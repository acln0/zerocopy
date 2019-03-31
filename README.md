zerocopy
================

`import "acln.ro/zerocopy"`

[![GoDoc](https://godoc.org/acln.ro/zerocopy?status.svg)](https://godoc.org/acln.ro/zerocopy)

Package zerocopy facilitates the construction of accelerated I/O
pipelines. Under circumstances where I/O acceleration is not
possible, the pipelines fall back to userspace data transfer
transparently. Currently, package zerocopy only offers accelerated
I/O on Linux, and for specific types of file descriptors.

Currently, package zerocopy is under active development. Bug reports
and contributions are welcome.

## Requirements

Package zerocopy requires at least Go 1.12. On the Go side of
things, package zerocopy uses type assertions on the `io.Reader`
and `io.Writer` arguments to `ReadFrom`, `WriteTo`, or `Transfer`,
in order to determine splice capabilities.

The first requirement is for the Go types to implement
the [syscall.Conn](https://golang.org/pkg/syscall/#Conn)
interface. Secondly, the `syscall.Conn` implementations must be
backed by real file descriptors, the file descriptors must be
set to non-blocking mode, and must be registered with the runtime
network poller.

More concretely, `*net.TCPConn` and `*net.UnixConn` (on the `"unix"`
network, with `SOCK_STREAM` semantics) should work out of the
box. Non-exotic varieties of `*os.File` should also work.

Generally, file descriptors involved in such transfers must be
stream-oriented. Stream-orientation is a necessary, but not sufficient
condition for `splice(2)` to work on a file descriptor. Consult the
appropriate subsystem manual, or the kernel source code, if you need
to be sure.

## Usage

Given the data flow described by the diagram

```
                     +-----------+
                     |           |
                     | recording |
                     |           |
                     +-----+-----+
                           ^
                           |
                           | file
                           |
+----------+       +-------+--------+       +----------+
|          |  TCP  |                |  TCP  |          |
|  camera  +------>+     server     +------>+  client  |
|          |       |                |       |          |
+----------+       +----------------+       +----------+
```

the server component could be implemented as follows:

```
func server(camera net.Conn, recording *os.File, client net.Conn) error {
	// Create a pipe between the camera and the client.
	campipe, err := zerocopy.NewPipe()
	if err != nil {
		return err
	}
	defer campipe.Close()

	// Create a pipe to the recording.
	recpipe, err := zerocopy.NewPipe()
	if err != nil {
		return err
	}
	defer recpipe.Close()

	// Arrange for data on campipe to be duplicated to recpipe.
	campipe.Tee(recpipe)

	// Run the world.
	go campipe.ReadFrom(camera)
	go recpipe.WriteTo(recording)
	go campipe.WriteTo(client)

	// ...
}
```

## License

Pacakge zerocopy is distributed under a BSD-style license. Apart from
the work belonging to the author, package zerocopy adapts and copies
Go standard library code and tests. See the LICENSE file, as well as
the individual copyright headers in source files.
