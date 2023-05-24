// Package archive: write, read, copy, append, list primitives
// across all supported formats
/*
 * Copyright (c) 2018-2023, NVIDIA CORPORATION. All rights reserved.
 */
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"sync"
	"time"

	"github.com/NVIDIA/aistore/cmn/cos"
	"github.com/NVIDIA/aistore/cmn/debug"
	"github.com/NVIDIA/aistore/cmn/feat"
	"github.com/NVIDIA/aistore/memsys"
	"github.com/pierrec/lz4/v3"
)

type (
	Writer interface {
		// Init specific writer
		Write(nameInArch string, oah cos.OAH, reader io.Reader) error
		// Close, cleanup
		Fini()
		// Copy arch, with potential subsequent APPEND
		Copy(src io.Reader, size ...int64) error

		// private
		init(w io.Writer, cksum *cos.CksumHashSize, serialize bool)
	}
	baseW struct {
		wmul io.Writer
		lck  sync.Locker // serialize: (multi-object => single shard)
		buf  []byte
		slab *memsys.Slab
	}
	tarWriter struct {
		baseW
		tw *tar.Writer
	}
	tgzWriter struct {
		tw  tarWriter
		gzw *gzip.Writer
	}
	zipWriter struct {
		baseW
		zw *zip.Writer
	}
	lz4Writer struct {
		tw  tarWriter
		lzw *lz4.Writer
	}
)

// interface guard
var (
	_ Writer = (*tarWriter)(nil)
	_ Writer = (*tgzWriter)(nil)
	_ Writer = (*zipWriter)(nil)
	_ Writer = (*lz4Writer)(nil)
)

func NewWriter(mime string, w io.Writer, cksum *cos.CksumHashSize, serialize bool) (aw Writer) {
	switch mime {
	case ExtTar:
		aw = &tarWriter{}
	case ExtTgz, ExtTarTgz:
		aw = &tgzWriter{}
	case ExtZip:
		aw = &zipWriter{}
	case ExtTarLz4:
		aw = &lz4Writer{}
	default:
		debug.Assert(false, mime)
	}
	aw.init(w, cksum, serialize)
	return
}

// baseW

func (bw *baseW) init(w io.Writer, cksum *cos.CksumHashSize, serialize bool) {
	bw.buf, bw.slab = memsys.PageMM().Alloc()
	if serialize {
		bw.lck = &sync.Mutex{}
	} else {
		bw.lck = cos.NopLocker{}
	}
	bw.wmul = w
	if cksum != nil {
		bw.wmul = cos.NewWriterMulti(w, cksum)
	}
}

// tarWriter

func (tw *tarWriter) init(w io.Writer, cksum *cos.CksumHashSize, serialize bool) {
	tw.baseW.init(w, cksum, serialize)
	tw.tw = tar.NewWriter(tw.wmul)
}

func (tw *tarWriter) Fini() {
	tw.slab.Free(tw.buf)
	tw.tw.Close()
}

func (tw *tarWriter) Write(fullname string, oah cos.OAH, reader io.Reader) (err error) {
	hdr := tar.Header{
		Typeflag: tar.TypeReg,
		Name:     fullname,
		Size:     oah.SizeBytes(),
		ModTime:  time.Unix(0, oah.AtimeUnix()),
	}
	SetAuxTarHeader(&hdr)
	tw.lck.Lock()
	if err = tw.tw.WriteHeader(&hdr); err == nil {
		_, err = io.CopyBuffer(tw.tw, reader, tw.buf)
	}
	tw.lck.Unlock()
	return
}

func (tw *tarWriter) Copy(src io.Reader, _ ...int64) error {
	return cpTar(src, tw.tw, tw.buf)
}

// tgzWriter

func (tzw *tgzWriter) init(w io.Writer, cksum *cos.CksumHashSize, serialize bool) {
	tzw.tw.baseW.init(w, cksum, serialize)
	tzw.gzw = gzip.NewWriter(tzw.tw.wmul)
	tzw.tw.tw = tar.NewWriter(tzw.gzw)
}

func (tzw *tgzWriter) Fini() {
	tzw.tw.Fini()
	tzw.gzw.Close()
}

func (tzw *tgzWriter) Write(fullname string, oah cos.OAH, reader io.Reader) error {
	return tzw.tw.Write(fullname, oah, reader)
}

func (tzw *tgzWriter) Copy(src io.Reader, _ ...int64) error {
	gzr, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	err = cpTar(gzr, tzw.tw.tw, tzw.tw.buf)
	cos.Close(gzr)
	return err
}

// zipWriter

func (zw *zipWriter) init(w io.Writer, cksum *cos.CksumHashSize, serialize bool) {
	zw.baseW.init(w, cksum, serialize)
	zw.zw = zip.NewWriter(zw.wmul)
}

func (zw *zipWriter) Fini() {
	zw.slab.Free(zw.buf)
	zw.zw.Close()
}

func (zw *zipWriter) Write(fullname string, oah cos.OAH, reader io.Reader) error {
	ziphdr := zip.FileHeader{
		Name:               fullname,
		Comment:            fullname,
		UncompressedSize64: uint64(oah.SizeBytes()),
		Modified:           time.Unix(0, oah.AtimeUnix()),
	}
	zw.lck.Lock()
	zipw, err := zw.zw.CreateHeader(&ziphdr)
	if err == nil {
		_, err = io.CopyBuffer(zipw, reader, zw.buf)
	}
	zw.lck.Unlock()
	return err
}

func (zw *zipWriter) Copy(src io.Reader, size ...int64) error {
	r, ok := src.(io.ReaderAt)
	debug.Assert(ok && len(size) == 1)
	return cpZip(r, size[0], zw.zw, zw.buf)
}

// lz4Writer

func (lzw *lz4Writer) init(w io.Writer, cksum *cos.CksumHashSize, serialize bool) {
	lzw.tw.baseW.init(w, cksum, serialize)
	lzw.lzw = lz4.NewWriter(lzw.tw.wmul)

	lzw.lzw.Header.BlockChecksum = false
	lzw.lzw.Header.NoChecksum = !features.IsSet(feat.LZ4FrameChecksum)
	lzw.lzw.Header.BlockMaxSize = 256 * cos.KiB
	if features.IsSet(feat.LZ4Block1MB) {
		lzw.lzw.Header.BlockMaxSize = cos.MiB
	}

	lzw.tw.tw = tar.NewWriter(lzw.lzw)
}

func (lzw *lz4Writer) Fini() {
	lzw.tw.Fini()
	lzw.lzw.Close()
}

func (lzw *lz4Writer) Write(fullname string, oah cos.OAH, reader io.Reader) error {
	return lzw.tw.Write(fullname, oah, reader)
}

func (lzw *lz4Writer) Copy(src io.Reader, _ ...int64) error {
	lzr := lz4.NewReader(src)
	return cpTar(lzr, lzw.tw.tw, lzw.tw.buf)
}