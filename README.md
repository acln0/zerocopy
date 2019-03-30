zerocopy
================

`import "acln.ro/zerocopy"`

[![GoDoc](https://godoc.org/acln.ro/zerocopy?status.svg)](https://godoc.org/acln.ro/zerocopy)

Package zerocopy facilitates the construction of accelerated I/O
pipelines. Under circumstances where I/O acceleration is not
possible, the pipelines fall back to userspace data transfer
transparently. Currently, package zerocopy only offers accelerated
I/O on Linux, and for specific types of file descriptors.

TODO(acln): document document what those file descriptors are, add
additional examples

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

Package zerocopy is under active development.