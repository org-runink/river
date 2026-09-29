package compress

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// A minimal io_uring (include/uapi/linux/io_uring.h) with raw system calls: just enough to
// keep a queue of IORING_OP_READ requests in flight, so the file-access test can compare it
// with read(2) without cgo or a dependency.

const (
	sysIOUringSetup = 425
	sysIOUringEnter = 426

	ioringOffSQRing = 0
	ioringOffCQRing = 0x8000000
	ioringOffSQEs   = 0x10000000

	ioringOpRead         = 22
	ioringEnterGetevents = 1
	ioringFeatSingleMmap = 1
	sqeSize              = 64
	cqeSize              = 16

	// struct io_uring_params: sq_entries@0, cq_entries@4, features@20, sq_off@40, cq_off@80.
	ioUringParamsSize = 120
	offSQEntries      = 0
	offCQEntries      = 4
	offFeatures       = 20
	offSQOff          = 40
	offCQOff          = 80
)

type ring struct {
	fd                     int
	sqRing, cqRing, sqes   []byte
	sqTail, sqMask         *uint32
	sqArray                unsafe.Pointer
	cqHead, cqTail, cqMask *uint32
	cqes                   unsafe.Pointer
	entries                uint32
	single                 bool
}

func u32(b []byte, off uint32) *uint32 { return (*uint32)(unsafe.Pointer(&b[off])) }

func newRing(entries uint32) (*ring, error) {
	var p [ioUringParamsSize]byte
	fd, _, e := syscall.Syscall(sysIOUringSetup, uintptr(entries), uintptr(unsafe.Pointer(&p[0])), 0)
	if e != 0 {
		return nil, fmt.Errorf("io_uring_setup: %w", e)
	}
	le := func(off int) uint32 { return *(*uint32)(unsafe.Pointer(&p[off])) }
	sqEntries, cqEntries := le(offSQEntries), le(offCQEntries)
	sqOff := func(i int) uint32 { return le(offSQOff + 4*i) } // head, tail, ring_mask, ring_entries, flags, dropped, array
	cqOff := func(i int) uint32 { return le(offCQOff + 4*i) } // head, tail, ring_mask, ring_entries, overflow, cqes
	sqSize := sqOff(6) + sqEntries*4
	cqSize := cqOff(5) + cqEntries*cqeSize
	r := &ring{fd: int(fd), entries: sqEntries, single: le(offFeatures)&ioringFeatSingleMmap != 0}
	if r.single {
		sqSize = max(sqSize, cqSize)
	}
	var err error
	prot, flags := syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_POPULATE
	if r.sqRing, err = syscall.Mmap(r.fd, ioringOffSQRing, int(sqSize), prot, flags); err != nil {
		syscall.Close(r.fd)
		return nil, err
	}
	r.cqRing = r.sqRing
	if !r.single {
		if r.cqRing, err = syscall.Mmap(r.fd, ioringOffCQRing, int(cqSize), prot, flags); err != nil {
			r.close()
			return nil, err
		}
	}
	if r.sqes, err = syscall.Mmap(r.fd, ioringOffSQEs, int(sqEntries*sqeSize), prot, flags); err != nil {
		r.close()
		return nil, err
	}
	r.sqTail, r.sqMask = u32(r.sqRing, sqOff(1)), u32(r.sqRing, sqOff(2))
	r.sqArray = unsafe.Pointer(&r.sqRing[sqOff(6)])
	r.cqHead, r.cqTail, r.cqMask = u32(r.cqRing, cqOff(0)), u32(r.cqRing, cqOff(1)), u32(r.cqRing, cqOff(2))
	r.cqes = unsafe.Pointer(&r.cqRing[cqOff(5)])
	return r, nil
}

func (r *ring) close() {
	if r.sqes != nil {
		_ = syscall.Munmap(r.sqes)
	}
	if !r.single && r.cqRing != nil {
		_ = syscall.Munmap(r.cqRing)
	}
	if r.sqRing != nil {
		_ = syscall.Munmap(r.sqRing)
	}
	syscall.Close(r.fd)
}

// queueRead puts one read on the submission queue (not yet submitted).
func (r *ring) queueRead(fd int, buf []byte, off int64, tag uint64) {
	tail := atomic.LoadUint32(r.sqTail)
	idx := tail & *r.sqMask
	sqe := r.sqes[idx*sqeSize : (idx+1)*sqeSize]
	clear(sqe)
	sqe[0] = ioringOpRead
	*(*int32)(unsafe.Pointer(&sqe[4])) = int32(fd)
	*(*uint64)(unsafe.Pointer(&sqe[8])) = uint64(off)
	*(*uint64)(unsafe.Pointer(&sqe[16])) = uint64(uintptr(unsafe.Pointer(&buf[0])))
	*(*uint32)(unsafe.Pointer(&sqe[24])) = uint32(len(buf))
	*(*uint64)(unsafe.Pointer(&sqe[32])) = tag
	*(*uint32)(unsafe.Add(r.sqArray, idx*4)) = idx
	atomic.StoreUint32(r.sqTail, tail+1)
}

// enter submits n queued entries and waits for at least wait completions.
func (r *ring) enter(n, wait uint32) error {
	for {
		_, _, e := syscall.Syscall6(sysIOUringEnter, uintptr(r.fd), uintptr(n), uintptr(wait), ioringEnterGetevents, 0, 0)
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return fmt.Errorf("io_uring_enter: %w", e)
		}
		return nil
	}
}

// reap hands every completed (tag, result) pair to f and returns how many there were.
func (r *ring) reap(f func(tag uint64, res int32)) int {
	head := atomic.LoadUint32(r.cqHead)
	tail := atomic.LoadUint32(r.cqTail)
	n := 0
	for ; head != tail; head++ {
		c := unsafe.Add(r.cqes, (head&*r.cqMask)*cqeSize)
		f(*(*uint64)(c), *(*int32)(unsafe.Add(c, 8)))
		n++
	}
	atomic.StoreUint32(r.cqHead, head)
	return n
}

// uringRead reads the given (offset, length) extents of fd with up to qd requests in
// flight and returns the bytes read.
func uringRead(fd int, extents [][2]int64, bufSize, qd int) (int64, error) {
	r, err := newRing(uint32(qd))
	if err != nil {
		return 0, err
	}
	defer r.close()
	qd = min(qd, int(r.entries))
	mem, err := syscall.Mmap(-1, 0, bufSize*qd, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return 0, err
	}
	defer syscall.Munmap(mem)
	free := make([]int, 0, qd)
	for i := 0; i < qd; i++ {
		free = append(free, i)
	}
	var total int64
	var firstErr error
	next, inflight := 0, 0
	for next < len(extents) || inflight > 0 {
		queued := uint32(0)
		for len(free) > 0 && next < len(extents) {
			slot := free[len(free)-1]
			free = free[:len(free)-1]
			e := extents[next]
			r.queueRead(fd, mem[slot*bufSize:slot*bufSize+int(e[1])], e[0], uint64(slot))
			next++
			inflight++
			queued++
		}
		if err := r.enter(queued, 1); err != nil {
			return total, err
		}
		inflight -= r.reap(func(tag uint64, res int32) {
			if res < 0 && firstErr == nil {
				firstErr = fmt.Errorf("io_uring read: %w", syscall.Errno(-res))
			}
			if res > 0 {
				total += int64(res)
			}
			free = append(free, int(tag))
		})
	}
	return total, firstErr
}
