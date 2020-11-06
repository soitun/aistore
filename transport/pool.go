// Package transport provides streaming object-based transport over http for intra-cluster continuous
// intra-cluster communications (see README for details and usage example).
/*
 * Copyright (c) 2018-2020, NVIDIA CORPORATION. All rights reserved.
 */
package transport

import (
	"io"
	"sync"

	"github.com/NVIDIA/aistore/cmn/debug"
)

///////////////////////////
// objReader pool (recv) //
///////////////////////////

var (
	recvPool        sync.Pool
	recvPoolEnabled bool // TODO -- FIXME: enable&remove
)

func allocRecv() (obj *objReader) {
	if recvPoolEnabled {
		if v := recvPool.Get(); v != nil {
			obj = v.(*objReader)
		}
	}
	return
}

func FreeRecv(reader io.Reader) {
	if recvPoolEnabled {
		obj := reader.(*objReader)
		debug.Assert(obj != nil)
		recvPool.Put(obj)
	}
}

/////////////////////
// Obj pool (send) //
/////////////////////

var sendPool sync.Pool

func AllocSend() (obj *Obj) {
	if v := sendPool.Get(); v != nil {
		obj = v.(*Obj)
		*obj = Obj{}
	} else {
		obj = &Obj{}
	}
	return
}

func freeSend(obj *Obj) { sendPool.Put(obj) }
