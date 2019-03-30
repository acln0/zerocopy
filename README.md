zerocopy
================

`import "acln.ro/zerocopy"`

[![GoDoc](https://godoc.org/acln.ro/zerocopy?status.svg)](https://godoc.org/acln.ro/zerocopy)

Package zerocopy facilitates the construction of accelerated I/O
pipelines. Under circumstances where I/O acceleration is not
possible, the pipelines fall back to userspace data transfer
transparently. Currently, package zerocopy only offers accelerated
I/O on Linux, and for specific types of file descriptors.

TODO(acln): document what those file descriptors are, add examples

Package zerocopy is under active development.
