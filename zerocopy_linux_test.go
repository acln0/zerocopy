// Copyright 2019 Andrei Tudor Călin
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

package zerocopy_test

import (
	"fmt"
	"io"
	"io/ioutil"
	"sync"
	"testing"

	"acln.ro/zerocopy"
)

func TestTeeRead(t *testing.T) {
	primary, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := zerocopy.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
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

	secondaries := make([]*zerocopy.Pipe, n)
	for i := 0; i < n; i++ {
		secondaries[i], err = zerocopy.NewPipe()
		if err != nil {
			t.Fatal(err)
		}
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
