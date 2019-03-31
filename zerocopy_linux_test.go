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
	"net"
	"sync"
	"testing"

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
	p, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var client net.Conn
	var dialerr error

	dialdone := make(chan struct{})

	go func() {
		client, dialerr = net.Dial(ln.Addr().Network(), ln.Addr().String())
		close(dialdone)
	}()

	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	<-dialdone
	if dialerr != nil {
		t.Fatal(dialerr)
	}
	defer client.Close()

	msg := "hello world"

	var clientwerr error
	var prerr error

	clientdone := make(chan struct{})
	prdone := make(chan struct{})

	go func() {
		defer close(clientdone)
		_, clientwerr = client.Write([]byte(msg))
		client.Close()
	}()

	go func() {
		defer close(prdone)
		buf := make([]byte, len(msg))
		_, prerr = io.ReadFull(p, buf)
		if prerr != nil {
			return
		}
		if string(buf) != msg {
			prerr = fmt.Errorf("got %q, want %q", string(buf), msg)
		}
	}()

	_, err = io.Copy(p, server)
	<-clientdone
	<-prdone

	if err != nil {
		t.Error(err)
	}
	if clientwerr != nil {
		t.Error(err)
	}
	if prerr != nil {
		t.Error(err)
	}
}

func TestWriteTo(t *testing.T) {
	p, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var client net.Conn
	var dialerr error

	dialdone := make(chan struct{})

	go func() {
		client, dialerr = net.Dial(ln.Addr().Network(), ln.Addr().String())
		close(dialdone)
	}()

	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	<-dialdone
	if dialerr != nil {
		t.Fatal(dialerr)
	}
	defer client.Close()

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

	_, err = io.Copy(server, p)
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

func TestTransfer(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var clientup net.Conn
	var uperr error

	updone := make(chan struct{})

	go func() {
		clientup, uperr = net.Dial(ln.Addr().Network(), ln.Addr().String())
		close(updone)
	}()

	serverup, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer serverup.Close()

	<-updone
	if uperr != nil {
		t.Fatal(err)
	}

	var clientdown net.Conn
	var downerr error

	downdone := make(chan struct{})

	go func() {
		clientdown, downerr = net.Dial(ln.Addr().Network(), ln.Addr().String())
		close(downdone)
	}()

	serverdown, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer serverdown.Close()

	<-downdone
	if downerr != nil {
		t.Fatal(err)
	}

	msg := "hello world"

	var werr error

	wdone := make(chan struct{})

	go func() {
		_, werr = clientup.Write([]byte(msg))
		clientup.Close()
		close(wdone)
	}()

	var rerr error

	rdone := make(chan struct{})

	go func() {
		defer close(rdone)
		buf := make([]byte, len(msg))
		_, rerr = io.ReadFull(clientdown, buf)
		if rerr != nil {
			return
		}
		if string(buf) != msg {
			rerr = fmt.Errorf("got %q, want %q", string(buf), msg)
		}
	}()

	_, err = zerocopy.Transfer(serverdown, serverup)
	if err != nil {
		t.Fatal(err)
	}
	<-wdone
	<-rdone
	if werr != nil {
		t.Error(werr)
	}
	if rerr != nil {
		t.Error(rerr)
	}
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

// TODO(acln): grab splice tests and benchmarks from the stdlib, use them here
