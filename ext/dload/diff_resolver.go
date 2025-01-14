// Package dload implements functionality to download resources into AIS cluster from external source.
/*
 * Copyright (c) 2018-2023, NVIDIA CORPORATION. All rights reserved.
 */
package dload

import (
	"github.com/NVIDIA/aistore/cluster"
	"github.com/NVIDIA/aistore/cmn"
	"github.com/NVIDIA/aistore/cmn/atomic"
	"github.com/NVIDIA/aistore/cmn/cos"
	"github.com/NVIDIA/aistore/cmn/debug"
	"github.com/NVIDIA/aistore/fs"
)

const (
	DiffResolverSend = iota
	DiffResolverRecv
	DiffResolverDelete
	DiffResolverSkip
	DiffResolverErr
	DiffResolverEOF
)

type (
	DiffResolverCtx interface {
		CompareObjects(*cluster.LOM, *DstElement) (bool, error)
		IsObjFromRemote(*cluster.LOM) (bool, error)
	}

	defaultDiffResolverCtx struct{}

	// DiffResolver is entity that computes difference between two streams
	// of objects. The streams are expected to be in sorted order.
	DiffResolver struct {
		ctx      DiffResolverCtx
		srcCh    chan *cluster.LOM
		dstCh    chan *DstElement
		resultCh chan DiffResolverResult
		err      cos.Errs
		stopped  atomic.Bool
	}

	BackendResource struct {
		ObjName string
	}

	WebResource struct {
		ObjName string
		Link    string
	}

	DstElement struct {
		ObjName string
		Version string
		Link    string
	}

	DiffResolverResult struct {
		Action uint8
		Src    *cluster.LOM
		Dst    *DstElement
		Err    error
	}
)

//////////////////
// DiffResolver //
//////////////////

func NewDiffResolver(ctx DiffResolverCtx) *DiffResolver {
	return &DiffResolver{
		ctx: ctx,
		// TODO: configurable size of the channels, plus `chanFull` check
		srcCh:    make(chan *cluster.LOM, 1000),
		dstCh:    make(chan *DstElement, 1000),
		resultCh: make(chan DiffResolverResult, 1000),
	}
}

func (dr *DiffResolver) Start() {
	defer close(dr.resultCh)
	src, srcOk := <-dr.srcCh
	dst, dstOk := <-dr.dstCh
	for {
		if !srcOk && !dstOk {
			dr.resultCh <- DiffResolverResult{
				Action: DiffResolverEOF,
			}
			return
		} else if !srcOk || (dstOk && src.ObjName > dst.ObjName) {
			dr.resultCh <- DiffResolverResult{
				Action: DiffResolverRecv,
				Dst:    dst,
			}
			dst, dstOk = <-dr.dstCh
		} else if !dstOk || (srcOk && src.ObjName < dst.ObjName) {
			remote, err := dr.ctx.IsObjFromRemote(src)
			if err != nil {
				dr.resultCh <- DiffResolverResult{
					Action: DiffResolverErr,
					Src:    src,
					Dst:    dst,
					Err:    err,
				}
				return
			}
			if remote {
				debug.Assert(!dstOk || dst.Link == "") // destination must be remote as well
				dr.resultCh <- DiffResolverResult{
					Action: DiffResolverDelete,
					Src:    src,
				}
			} else {
				dr.resultCh <- DiffResolverResult{
					Action: DiffResolverSend,
					Src:    src,
				}
			}
			src, srcOk = <-dr.srcCh
		} else { /* s.ObjName == d.ObjName */
			equal, err := dr.ctx.CompareObjects(src, dst)
			if err != nil {
				dr.resultCh <- DiffResolverResult{
					Action: DiffResolverErr,
					Src:    src,
					Dst:    dst,
					Err:    err,
				}
				return
			}
			if equal {
				dr.resultCh <- DiffResolverResult{
					Action: DiffResolverSkip,
					Src:    src,
					Dst:    dst,
				}
			} else {
				dr.resultCh <- DiffResolverResult{
					Action: DiffResolverRecv,
					Dst:    dst,
				}
			}
			src, srcOk = <-dr.srcCh
			dst, dstOk = <-dr.dstCh
		}
	}
}

func (dr *DiffResolver) PushSrc(v any) {
	switch x := v.(type) {
	case *cluster.LOM:
		dr.srcCh <- x
	default:
		debug.FailTypeCast(v)
	}
}

func (dr *DiffResolver) CloseSrc() { close(dr.srcCh) }

func (dr *DiffResolver) PushDst(v any) {
	var d *DstElement
	switch x := v.(type) {
	case *BackendResource:
		d = &DstElement{
			ObjName: x.ObjName,
		}
	case *WebResource:
		d = &DstElement{
			ObjName: x.ObjName,
			Link:    x.Link,
		}
	default:
		debug.FailTypeCast(v)
	}

	dr.dstCh <- d
}

func (dr *DiffResolver) CloseDst() { close(dr.dstCh) }

func (dr *DiffResolver) Next() (DiffResolverResult, error) {
	if cnt, err := dr.err.JoinErr(); cnt > 0 {
		return DiffResolverResult{}, err
	}
	r, ok := <-dr.resultCh
	if !ok {
		return DiffResolverResult{Action: DiffResolverEOF}, nil
	}
	return r, nil
}

func (dr *DiffResolver) Stop()           { dr.stopped.Store(true) }
func (dr *DiffResolver) Stopped() bool   { return dr.stopped.Load() }
func (dr *DiffResolver) Abort(err error) { dr.err.Add(err) }

func (dr *DiffResolver) walk(job jobif) {
	defer dr.CloseSrc()
	opts := &fs.WalkBckOpts{
		WalkOpts: fs.WalkOpts{CTs: []string{fs.ObjectType}, Sorted: true},
	}
	opts.WalkOpts.Bck.Copy(job.Bck())
	opts.Callback = func(fqn string, de fs.DirEntry) error {
		if dr.Stopped() {
			return cmn.NewErrAborted(job.String(), "diff-resolver stopped", nil)
		}
		lom := &cluster.LOM{}
		if err := lom.InitFQN(fqn, job.Bck()); err != nil {
			return err
		}
		if !job.checkObj(lom.ObjName) {
			return nil
		}
		dr.PushSrc(lom)
		return nil
	}
	err := fs.WalkBck(opts)
	if err != nil && !cmn.IsErrAborted(err) {
		dr.Abort(err)
	}
}

func (dr *DiffResolver) push(job jobif, d *dispatcher) {
	defer func() {
		dr.CloseDst()
		if !job.Sync() {
			dr.CloseSrc()
		}
	}()

	for {
		objs, ok, err := job.genNext()
		if err != nil {
			dr.Abort(err)
			return
		}
		if !ok || dr.Stopped() {
			return
		}
		for _, obj := range objs {
			if d.checkAborted() {
				err := cmn.NewErrAborted(job.String(), "", nil)
				dr.Abort(err)
				return
			}
			if d.checkAbortedJob(job) {
				dr.Stop()
				return
			}
			if !job.Sync() {
				// When it is not a sync job, push LOM for a given object
				// because we need to check if it exists.
				lom := &cluster.LOM{ObjName: obj.objName}
				if err := lom.InitBck(job.Bck()); err != nil {
					dr.Abort(err)
					return
				}
				dr.PushSrc(lom)
			}
			if obj.link != "" {
				dr.PushDst(&WebResource{
					ObjName: obj.objName,
					Link:    obj.link,
				})
			} else {
				dr.PushDst(&BackendResource{
					ObjName: obj.objName,
				})
			}
		}
	}
}

////////////////////////////
// defaultDiffResolverCtx //
////////////////////////////

func (*defaultDiffResolverCtx) CompareObjects(src *cluster.LOM, dst *DstElement) (bool, error) {
	if err := src.Load(true /*cache it*/, false /*locked*/); err != nil {
		if cmn.IsObjNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return CompareObjects(src, dst)
}

func (*defaultDiffResolverCtx) IsObjFromRemote(src *cluster.LOM) (bool, error) {
	if err := src.Load(true /*cache it*/, false /*locked*/); err != nil {
		if cmn.IsObjNotExist(err) {
			return false, nil
		}
		return false, err
	}
	objSrc, ok := src.GetCustomKey(cmn.SourceObjMD)
	if !ok {
		return false, nil
	}
	return objSrc != cmn.WebObjMD, nil
}
