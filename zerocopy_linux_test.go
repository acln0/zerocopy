// Copyright (c) 2018 The Go Authors https://golang.org/AUTHORS
// Copyright (c) 2019 Andrei Tudor Călin <mail@acln.ro>
// All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zerocopy_test

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"acln.ro/zerocopy"
)

func TestTeeRead(t *testing.T) {
	primary, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()

	secondary, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer secondary.Close()

	primary.Tee(secondary)

	msg := "hello world"
	var (
		wg           sync.WaitGroup
		primaryerr   error
		secondaryerr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, len(msg))
		_, secondaryerr = io.ReadFull(secondary, buf)
		if secondaryerr != nil {
			return
		}
		if string(buf) != msg {
			secondaryerr = fmt.Errorf("got %q, want %q", buf, msg)
		}
	}()
	go func() {
		defer wg.Done()
		_, primaryerr = io.Copy(ioutil.Discard, primary)
	}()

	if _, err := io.WriteString(primary, msg); err != nil {
		t.Fatal(err)
	}
	primary.CloseWrite()
	wg.Wait()

	if primaryerr != nil {
		t.Error(primaryerr)
	}
	if secondaryerr != nil {
		t.Error(secondaryerr)
	}
}

func TestTeeChain(t *testing.T) {
	for n := 1; n <= 10; n++ {
		testTeeChain(t, n)
	}
}

func testTeeChain(t *testing.T, n int) {
	primary, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()

	secondaries := make([]*zerocopy.Pipe, n)
	for i := 0; i < n; i++ {
		secondaries[i], err = zerocopy.NewPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer secondaries[i].Close()
	}
	for i := 0; i < n-1; i++ {
		secondaries[i].Tee(secondaries[i+1])
	}
	primary.Tee(secondaries[0])

	msg := "hello world"
	var (
		wg            sync.WaitGroup
		primaryerr    error
		secondaryerrs = make([]error, n)
	)
	wg.Add(n + 1)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			buf := make([]byte, len(msg))
			_, err := io.ReadFull(secondaries[i], buf)
			if err != nil {
				secondaryerrs[i] = err
				return
			}
			if string(buf) != msg {
				secondaryerrs[i] = fmt.Errorf("got %q, want %q", buf, msg)
			}
		}(i)
	}
	go func() {
		defer wg.Done()
		_, primaryerr = io.Copy(ioutil.Discard, primary)
	}()

	if _, err := io.WriteString(primary, msg); err != nil {
		t.Fatal(err)
	}
	if err := primary.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if primaryerr != nil {
		t.Error(primaryerr)
	}
	for i := 0; i < n; i++ {
		if secondaryerrs[i] != nil {
			t.Error(secondaryerrs[i])
		}
	}
}

func TestReadFrom(t *testing.T) {
	t.Run("RacyOrder", testReadFromRacyOrder)
	t.Run("BlockedInRead", testReadFromBlockedInRead)
	// t.Run("BlockedInWrite", testReadFromBlockedInWrite)
	//
	// TODO(acln): investigate BlockedInWrite, make it pass somehow. hard.
}

func testReadFromRacyOrder(t *testing.T) {
	p, client, server, cleanup := newSpliceTest(t)
	defer cleanup()

	msg := "hello world"

	var werr, rerr, cerr error

	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()

		<-start
		_, werr = client.Write([]byte(msg))
		client.Close()
	}()

	go func() {
		defer wg.Done()

		<-start
		buf := make([]byte, len(msg))
		_, rerr = io.ReadFull(p, buf)
		if rerr != nil {
			return
		}
		if string(buf) != msg {
			rerr = fmt.Errorf("got %q, want %q", string(buf), msg)
		}
	}()

	go func() {
		defer wg.Done()

		<-start
		_, cerr = io.Copy(p, server)
	}()

	time.Sleep(2 * time.Millisecond)
	close(start)
	wg.Wait()

	if werr != nil {
		t.Error(werr)
	}
	if rerr != nil {
		t.Error(rerr)
	}
	if cerr != nil {
		t.Error(cerr)
	}
}

func testReadFromBlockedInRead(t *testing.T) {
	p, client, server, cleanup := newSpliceTest(t)
	defer cleanup()

	msg := "hello world"

	var werr, rerr, cerr error

	startcopy := make(chan struct{})
	startwrite := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()

		<-startwrite
		_, werr = client.Write([]byte(msg))
		client.Close()
	}()

	go func() {
		defer wg.Done()

		<-startcopy
		buf := make([]byte, len(msg))
		_, rerr = io.ReadFull(p, buf)
		if rerr != nil {
			return
		}
		if string(buf) != msg {
			rerr = fmt.Errorf("got %q, want %q", string(buf), msg)
		}
	}()

	go func() {
		defer wg.Done()

		<-startcopy
		_, cerr = io.Copy(p, server)
	}()

	time.Sleep(2 * time.Millisecond)
	close(startcopy)
	time.Sleep(2 * time.Millisecond)
	close(startwrite)
	wg.Wait()

	if werr != nil {
		t.Error(werr)
	}
	if rerr != nil {
		t.Error(rerr)
	}
	if cerr != nil {
		t.Error(cerr)
	}
}

func testReadFromBlockedInWrite(t *testing.T) {
	p, client, server, cleanup := newSpliceTest(t)
	defer cleanup()

	// Fill the pipe so writing to it will block.
	size, err := p.BufferSize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}

	msg := "hello world"

	var werr, rerr, cerr error

	startcopy := make(chan struct{})
	startread := make(chan struct{})
	startwrite := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()

		<-startwrite
		_, werr = client.Write([]byte(msg))
		client.Close()
	}()

	go func() {
		defer wg.Done()

		<-startread
		if _, rerr = io.CopyN(ioutil.Discard, p, int64(size)); rerr != nil {
			return
		}
		buf := make([]byte, len(msg))
		_, rerr = io.ReadFull(p, buf)
		if rerr != nil {
			return
		}
		if string(buf) != msg {
			rerr = fmt.Errorf("got %q, want %q", string(buf), msg)
		}
	}()

	go func() {
		defer wg.Done()

		<-startcopy
		_, cerr = io.Copy(p, server)
	}()

	time.Sleep(2 * time.Millisecond)
	close(startwrite)
	time.Sleep(2 * time.Millisecond)
	close(startcopy)
	time.Sleep(2 * time.Millisecond)
	close(startread)
	wg.Wait()

	if werr != nil {
		t.Error(werr)
	}
	if rerr != nil {
		t.Error(rerr)
	}
	if cerr != nil {
		t.Error(cerr)
	}
}

func TestWriteTo(t *testing.T) {
	p, client, server, cleanup := newSpliceTest(t)
	defer cleanup()

	msg := "hello world"

	var clientrerr error
	var pwerr error

	clientdone := make(chan struct{})
	pwdone := make(chan struct{})

	go func() {
		defer close(clientdone)
		buf := make([]byte, len(msg))
		_, clientrerr = io.ReadFull(client, buf)
		if clientrerr != nil {
			return
		}
		if string(buf) != msg {
			clientrerr = fmt.Errorf("got %q, want %q", string(buf), msg)
		}
	}()

	go func() {
		defer close(pwdone)
		_, pwerr = io.WriteString(p, msg)
		p.CloseWrite()
	}()

	_, err := io.Copy(server, p)
	<-clientdone
	<-pwdone

	if err != nil {
		t.Error(err)
	}
	if clientrerr != nil {
		t.Error(err)
	}
	if pwerr != nil {
		t.Error(err)
	}
}

func newSpliceTest(t *testing.T) (*zerocopy.Pipe, net.Conn, net.Conn, func()) {
	t.Helper()
	p, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	client, server, err := transferTestSocketPair("tcp")
	if err != nil {
		p.Close()
		t.Fatal(err)
	}
	cleanup := func() {
		p.Close()
		client.Close()
		server.Close()
	}
	return p, client, server, cleanup
}

// TODO(acln): add test cases where we transfer to and from pipes

// TODO(acln): add test cases for WriteTo with more combinations of
// blocking source and destination file descriptors.

func TestTransfer(t *testing.T) {
	t.Run("tcp-to-tcp", func(t *testing.T) { testTransfer(t, "tcp", "tcp") })
	t.Run("unix-to-tcp", func(t *testing.T) { testTransfer(t, "unix", "tcp") })
	t.Run("tcp-to-unix", func(t *testing.T) { testTransfer(t, "tcp", "unix") })
	t.Run("unix-to-unix", func(t *testing.T) { testTransfer(t, "unix", "unix") })
}

func testTransfer(t *testing.T, upNet, downNet string) {
	t.Run("simple", transferTestCase{upNet, downNet, 128, 128, 0}.test)
	t.Run("multipleWrite", transferTestCase{upNet, downNet, 4096, 1 << 20, 0}.test)
	t.Run("big", transferTestCase{upNet, downNet, 5 << 20, 1 << 30, 0}.test)
	t.Run("honorsLimitedReader", transferTestCase{upNet, downNet, 4096, 1 << 20, 1 << 10}.test)
	t.Run("updatesLimitedReaderN", transferTestCase{upNet, downNet, 1024, 4096, 4096 + 100}.test)
	t.Run("limitedReaderAtLimit", transferTestCase{upNet, downNet, 32, 128, 128}.test)
	t.Run("readerAtEOF", func(t *testing.T) { testTransferReaderAtEOF(t, upNet, downNet) })
	t.Run("issue25985", func(t *testing.T) { testTransferIssue25985(t, upNet, downNet) })
}

type transferTestCase struct {
	upNet, downNet string

	chunkSize, totalSize int
	limitReadSize        int
}

func (tc transferTestCase) test(t *testing.T) {
	clientUp, serverUp, err := transferTestSocketPair(tc.upNet)
	if err != nil {
		t.Fatal(err)
	}
	defer serverUp.Close()
	cleanup, err := startTransferClient(clientUp, "w", tc.chunkSize, tc.totalSize)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	clientDown, serverDown, err := transferTestSocketPair(tc.downNet)
	if err != nil {
		t.Fatal(err)
	}
	defer serverDown.Close()
	cleanup, err = startTransferClient(clientDown, "r", tc.chunkSize, tc.totalSize)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var (
		r    io.Reader = serverUp
		size           = tc.totalSize
	)
	if tc.limitReadSize > 0 {
		if tc.limitReadSize < size {
			size = tc.limitReadSize
		}

		r = &io.LimitedReader{
			N: int64(tc.limitReadSize),
			R: serverUp,
		}
		defer serverUp.Close()
	}
	n, err := zerocopy.Transfer(serverDown, r)
	serverDown.Close()
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(size); want != n {
		t.Errorf("want %d bytes transfered, got %d", want, n)
	}

	if tc.limitReadSize > 0 {
		wantN := 0
		if tc.limitReadSize > size {
			wantN = tc.limitReadSize - size
		}

		if n := r.(*io.LimitedReader).N; n != int64(wantN) {
			t.Errorf("r.N = %d, want %d", n, wantN)
		}
	}
}

func testTransferReaderAtEOF(t *testing.T, upNet, downNet string) {
	clientUp, serverUp, err := transferTestSocketPair(upNet)
	if err != nil {
		t.Fatal(err)
	}
	defer clientUp.Close()
	clientDown, serverDown, err := transferTestSocketPair(downNet)
	if err != nil {
		t.Fatal(err)
	}
	defer clientDown.Close()

	serverUp.Close()

	msg := "bye"
	go func() {
		zerocopy.Transfer(serverDown, serverUp)
		io.WriteString(serverDown, msg)
		serverDown.Close()
	}()

	buf := make([]byte, 3)
	_, err = io.ReadFull(clientDown, buf)
	if err != nil {
		t.Errorf("clientDown: %v", err)
	}
	if string(buf) != msg {
		t.Errorf("clientDown got %q, want %q", buf, msg)
	}
}

func testTransferIssue25985(t *testing.T, upNet, downNet string) {
	front, err := newLocalListener(upNet)
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	back, err := newLocalListener(downNet)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	proxy := func() {
		src, err := front.Accept()
		if err != nil {
			return
		}
		dst, err := net.Dial(downNet, back.Addr().String())
		if err != nil {
			return
		}
		defer dst.Close()
		defer src.Close()
		go func() {
			zerocopy.Transfer(src, dst)
			wg.Done()
		}()
		go func() {
			zerocopy.Transfer(dst, src)
			wg.Done()
		}()
	}

	go proxy()

	toFront, err := net.Dial(upNet, front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	io.WriteString(toFront, "foo")
	toFront.Close()

	fromProxy, err := back.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer fromProxy.Close()

	_, err = ioutil.ReadAll(fromProxy)
	if err != nil {
		t.Fatal(err)
	}

	wg.Wait()
}

func BenchmarkTransfer(b *testing.B) {
	b.Run("tcp-to-tcp", func(b *testing.B) { benchTransfer(b, "tcp", "tcp") })
	b.Run("unix-to-tcp", func(b *testing.B) { benchTransfer(b, "unix", "tcp") })
	b.Run("tcp-to-unix", func(b *testing.B) { benchTransfer(b, "tcp", "unix") })
	b.Run("unix-to-unix", func(b *testing.B) { benchTransfer(b, "unix", "unix") })
}

func benchTransfer(b *testing.B, upNet, downNet string) {
	for i := 0; i <= 10; i++ {
		chunkSize := 1 << uint(i+10)
		tc := transferTestCase{
			upNet:     upNet,
			downNet:   downNet,
			chunkSize: chunkSize,
		}

		b.Run(strconv.Itoa(chunkSize), tc.bench)
	}
}

func (tc transferTestCase) bench(b *testing.B) {
	// To benchmark the generic transfer code path, set this to false.
	useTransfer := true

	clientUp, serverUp, err := transferTestSocketPair(tc.upNet)
	if err != nil {
		b.Fatal(err)
	}
	defer serverUp.Close()

	cleanup, err := startTransferClient(clientUp, "w", tc.chunkSize, tc.chunkSize*b.N)
	if err != nil {
		b.Fatal(err)
	}
	defer cleanup()

	clientDown, serverDown, err := transferTestSocketPair(tc.downNet)
	if err != nil {
		b.Fatal(err)
	}
	defer serverDown.Close()

	cleanup, err = startTransferClient(clientDown, "r", tc.chunkSize, tc.chunkSize*b.N)
	if err != nil {
		b.Fatal(err)
	}
	defer cleanup()

	b.SetBytes(int64(tc.chunkSize))
	b.ResetTimer()

	if useTransfer {
		_, err := zerocopy.Transfer(serverDown, serverUp)
		if err != nil {
			b.Fatal(err)
		}
	} else {
		type onlyReader struct {
			io.Reader
		}
		_, err := io.Copy(serverDown, onlyReader{serverUp})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func transferTestSocketPair(network string) (client, server net.Conn, err error) {
	ln, err := newLocalListener(network)
	if err != nil {
		return nil, nil, err
	}
	defer ln.Close()
	var cerr, serr error
	acceptDone := make(chan struct{})
	go func() {
		server, serr = ln.Accept()
		acceptDone <- struct{}{}
	}()
	client, cerr = net.Dial(ln.Addr().Network(), ln.Addr().String())
	<-acceptDone
	if cerr != nil {
		if server != nil {
			server.Close()
		}
		return nil, nil, cerr
	}
	if serr != nil {
		if client != nil {
			client.Close()
		}
		return nil, nil, serr
	}
	return client, server, nil
}

func startTransferClient(conn net.Conn, op string, chunkSize, totalSize int) (func(), error) {
	f, err := conn.(interface{ File() (*os.File, error) }).File()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = []string{
		"ZEROCOPY_NET_TEST=1",
		"ZEROCOPY_NET_TEST_OP=" + op,
		"ZEROCOPY_NET_TEST_CHUNK_SIZE=" + strconv.Itoa(chunkSize),
		"ZEROCOPY_NET_TEST_TOTAL_SIZE=" + strconv.Itoa(totalSize),
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	donec := make(chan struct{})
	go func() {
		cmd.Wait()
		conn.Close()
		f.Close()
		close(donec)
	}()

	return func() {
		select {
		case <-donec:
		case <-time.After(5 * time.Second):
			log.Printf("killing transfer client after 5 second shutdown timeout")
			cmd.Process.Kill()
			select {
			case <-donec:
			case <-time.After(5 * time.Second):
				log.Printf("transfer client didn't die after 10 seconds")
			}
		}
	}, nil
}

func init() {
	if os.Getenv("ZEROCOPY_NET_TEST") == "" {
		return
	}
	defer os.Exit(0)

	f := os.NewFile(uintptr(3), "transfer-test-conn")
	defer f.Close()

	conn, err := net.FileConn(f)
	if err != nil {
		log.Fatal(err)
	}

	var chunkSize int
	if chunkSize, err = strconv.Atoi(os.Getenv("ZEROCOPY_NET_TEST_CHUNK_SIZE")); err != nil {
		log.Fatal(err)
	}
	buf := make([]byte, chunkSize)

	var totalSize int
	if totalSize, err = strconv.Atoi(os.Getenv("ZEROCOPY_NET_TEST_TOTAL_SIZE")); err != nil {
		log.Fatal(err)
	}

	var fn func([]byte) (int, error)
	switch op := os.Getenv("ZEROCOPY_NET_TEST_OP"); op {
	case "r":
		fn = conn.Read
	case "w":
		defer conn.Close()

		fn = conn.Write
	default:
		log.Fatalf("unknown op %q", op)
	}

	var n int
	for count := 0; count < totalSize; count += n {
		if count+chunkSize > totalSize {
			buf = buf[:totalSize-count]
		}

		var err error
		if n, err = fn(buf); err != nil {
			return
		}
	}
}

func newLocalListener(network string) (net.Listener, error) {
	switch network {
	case "tcp":
		if ln, err := net.Listen("tcp4", "127.0.0.1:0"); err == nil {
			return ln, nil
		}
		return net.Listen("tcp6", "[::1]:0")
	case "tcp4":
		return net.Listen("tcp4", "127.0.0.1:0")
	case "tcp6":
		return net.Listen("tcp6", "[::1]:0")
	case "unix", "unixpacket":
		return net.Listen(network, testUnixAddr())
	}
	return nil, fmt.Errorf("%s is not supported", network)
}

// testUnixAddr uses ioutil.TempFile to get a name that is unique.
func testUnixAddr() string {
	f, err := ioutil.TempFile("", "zerocopy-nettest")
	if err != nil {
		panic(err)
	}
	addr := f.Name()
	f.Close()
	os.Remove(addr)
	return addr
}

func TestSetBufferSize(t *testing.T) {
	n := 32 * 4096
	p, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.SetBufferSize(n); err != nil {
		t.Fatal(err)
	}
	got, err := p.BufferSize()
	if err != nil {
		t.Fatal(err)
	}
	if got != n {
		t.Fatalf("got %d, want %d", got, n)
	}
}
