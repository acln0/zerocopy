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

Consider this diagram of a proxy server:

```
+----------+       +----------------+       +------------+
|          +<------+                +<------+            |
| upstream |   P   |  proxy server  |   Q   | downstream |
|          +------->                +------->            |
+----------+       +----------------+       +------------+
```

`P` and `Q` represent streaming communication protocols, for example
TCP, or a streaming UNIX domain socket. Implementing this proxy server
is straightforward:


```
func proxy(upstream, downstream net.Conn) error {
	go zerocopy.Transfer(upstream, downstream)
	go zerocopy.Transfer(downstream, upstream)

	// ... wait, clean up, etc.
}
```

---

Consider now a slightly more complex data flow diagram.

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

This server component could be implemented as follows:

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

	// ... wait, clean up etc.
}
```

## Additional reading

* [man 2 splice](http://man7.org/linux/man-pages/man2/splice.2.html)
* [man 2 tee](http://man7.org/linux/man-pages/man2/tee.2.html)
* [Linus Torvalds on splice(2) and tee(2)](https://yarchive.net/comp/linux/splice.html)

## Benchmarks

### `Transfer` - in userspace vs. splice-accelerated

```
benchmark                                    old ns/op     new ns/op     delta
BenchmarkTransfer/tcp-to-tcp/1024-4          3972          3667          -7.68%
BenchmarkTransfer/tcp-to-tcp/2048-4          3361          3193          -5.00%
BenchmarkTransfer/tcp-to-tcp/4096-4          3522          3613          +2.58%
BenchmarkTransfer/tcp-to-tcp/8192-4          4400          3840          -12.73%
BenchmarkTransfer/tcp-to-tcp/16384-4         6293          4038          -35.83%
BenchmarkTransfer/tcp-to-tcp/32768-4         13637         5465          -59.93%
BenchmarkTransfer/tcp-to-tcp/65536-4         22652         10155         -55.17%
BenchmarkTransfer/tcp-to-tcp/131072-4        44927         17892         -60.18%
BenchmarkTransfer/tcp-to-tcp/262144-4        111338        37230         -66.56%
BenchmarkTransfer/tcp-to-tcp/524288-4        227587        77118         -66.11%
BenchmarkTransfer/tcp-to-tcp/1048576-4       482285        295034        -38.83%
BenchmarkTransfer/unix-to-tcp/1024-4         1173          1342          +14.41%
BenchmarkTransfer/unix-to-tcp/2048-4         1376          1478          +7.41%
BenchmarkTransfer/unix-to-tcp/4096-4         2337          1814          -22.38%
BenchmarkTransfer/unix-to-tcp/8192-4         2879          2167          -24.73%
BenchmarkTransfer/unix-to-tcp/16384-4        5353          3422          -36.07%
BenchmarkTransfer/unix-to-tcp/32768-4        9816          5651          -42.43%
BenchmarkTransfer/unix-to-tcp/65536-4        20921         11871         -43.26%
BenchmarkTransfer/unix-to-tcp/131072-4       37644         21996         -41.57%
BenchmarkTransfer/unix-to-tcp/262144-4       76739         44324         -42.24%
BenchmarkTransfer/unix-to-tcp/524288-4       148243        78678         -46.93%
BenchmarkTransfer/unix-to-tcp/1048576-4      311495        151634        -51.32%
BenchmarkTransfer/tcp-to-unix/1024-4         4258          3993          -6.22%
BenchmarkTransfer/tcp-to-unix/2048-4         3268          3299          +0.95%
BenchmarkTransfer/tcp-to-unix/4096-4         3572          3253          -8.93%
BenchmarkTransfer/tcp-to-unix/8192-4         2776          3737          +34.62%
BenchmarkTransfer/tcp-to-unix/16384-4        4556          4989          +9.50%
BenchmarkTransfer/tcp-to-unix/32768-4        10038         6707          -33.18%
BenchmarkTransfer/tcp-to-unix/65536-4        19597         10823         -44.77%
BenchmarkTransfer/tcp-to-unix/131072-4       40673         15997         -60.67%
BenchmarkTransfer/tcp-to-unix/262144-4       76247         30427         -60.09%
BenchmarkTransfer/tcp-to-unix/524288-4       148763        60432         -59.38%
BenchmarkTransfer/tcp-to-unix/1048576-4      307637        117157        -61.92%
BenchmarkTransfer/unix-to-unix/1024-4        1090          1102          +1.10%
BenchmarkTransfer/unix-to-unix/2048-4        1094          1111          +1.55%
BenchmarkTransfer/unix-to-unix/4096-4        1558          1591          +2.12%
BenchmarkTransfer/unix-to-unix/8192-4        2013          2597          +29.01%
BenchmarkTransfer/unix-to-unix/16384-4       3373          2765          -18.03%
BenchmarkTransfer/unix-to-unix/32768-4       6864          3327          -51.53%
BenchmarkTransfer/unix-to-unix/65536-4       14177         9587          -32.38%
BenchmarkTransfer/unix-to-unix/131072-4      26012         17204         -33.86%
BenchmarkTransfer/unix-to-unix/262144-4      48500         31884         -34.26%
BenchmarkTransfer/unix-to-unix/524288-4      102363        62569         -38.88%
BenchmarkTransfer/unix-to-unix/1048576-4     204508        131515        -35.69%

benchmark                                    old MB/s     new MB/s     speedup
BenchmarkTransfer/tcp-to-tcp/1024-4          257.77       279.21       1.08x
BenchmarkTransfer/tcp-to-tcp/2048-4          609.32       641.23       1.05x
BenchmarkTransfer/tcp-to-tcp/4096-4          1162.72      1133.52      0.97x
BenchmarkTransfer/tcp-to-tcp/8192-4          1861.72      2132.81      1.15x
BenchmarkTransfer/tcp-to-tcp/16384-4         2603.52      4057.02      1.56x
BenchmarkTransfer/tcp-to-tcp/32768-4         2402.79      5995.63      2.50x
BenchmarkTransfer/tcp-to-tcp/65536-4         2893.13      6453.09      2.23x
BenchmarkTransfer/tcp-to-tcp/131072-4        2917.40      7325.41      2.51x
BenchmarkTransfer/tcp-to-tcp/262144-4        2354.48      7041.18      2.99x
BenchmarkTransfer/tcp-to-tcp/524288-4        2303.68      6798.46      2.95x
BenchmarkTransfer/tcp-to-tcp/1048576-4       2174.18      3554.08      1.63x
BenchmarkTransfer/unix-to-tcp/1024-4         872.62       762.84       0.87x
BenchmarkTransfer/unix-to-tcp/2048-4         1487.48      1385.61      0.93x
BenchmarkTransfer/unix-to-tcp/4096-4         1752.30      2257.64      1.29x
BenchmarkTransfer/unix-to-tcp/8192-4         2844.91      3780.21      1.33x
BenchmarkTransfer/unix-to-tcp/16384-4        3060.64      4787.20      1.56x
BenchmarkTransfer/unix-to-tcp/32768-4        3337.99      5797.91      1.74x
BenchmarkTransfer/unix-to-tcp/65536-4        3132.48      5520.36      1.76x
BenchmarkTransfer/unix-to-tcp/131072-4       3481.81      5958.82      1.71x
BenchmarkTransfer/unix-to-tcp/262144-4       3416.03      5914.26      1.73x
BenchmarkTransfer/unix-to-tcp/524288-4       3536.66      6663.65      1.88x
BenchmarkTransfer/unix-to-tcp/1048576-4      3366.26      6915.13      2.05x
BenchmarkTransfer/tcp-to-unix/1024-4         240.46       256.40       1.07x
BenchmarkTransfer/tcp-to-unix/2048-4         626.59       620.76       0.99x
BenchmarkTransfer/tcp-to-unix/4096-4         1146.67      1258.78      1.10x
BenchmarkTransfer/tcp-to-unix/8192-4         2950.44      2191.74      0.74x
BenchmarkTransfer/tcp-to-unix/16384-4        3596.05      3283.65      0.91x
BenchmarkTransfer/tcp-to-unix/32768-4        3264.14      4884.94      1.50x
BenchmarkTransfer/tcp-to-unix/65536-4        3344.10      6055.24      1.81x
BenchmarkTransfer/tcp-to-unix/131072-4       3222.57      8193.26      2.54x
BenchmarkTransfer/tcp-to-unix/262144-4       3438.06      8615.32      2.51x
BenchmarkTransfer/tcp-to-unix/524288-4       3524.30      8675.60      2.46x
BenchmarkTransfer/tcp-to-unix/1048576-4      3408.48      8950.12      2.63x
BenchmarkTransfer/unix-to-unix/1024-4        939.04       929.05       0.99x
BenchmarkTransfer/unix-to-unix/2048-4        1871.53      1842.52      0.98x
BenchmarkTransfer/unix-to-unix/4096-4        2627.44      2574.18      0.98x
BenchmarkTransfer/unix-to-unix/8192-4        4068.58      3153.97      0.78x
BenchmarkTransfer/unix-to-unix/16384-4       4856.35      5925.29      1.22x
BenchmarkTransfer/unix-to-unix/32768-4       4773.31      9846.81      2.06x
BenchmarkTransfer/unix-to-unix/65536-4       4622.41      6835.42      1.48x
BenchmarkTransfer/unix-to-unix/131072-4      5038.74      7618.43      1.51x
BenchmarkTransfer/unix-to-unix/262144-4      5405.02      8221.65      1.52x
BenchmarkTransfer/unix-to-unix/524288-4      5121.83      8379.33      1.64x
BenchmarkTransfer/unix-to-unix/1048576-4     5127.30      7972.99      1.56x
```

### `Tee`

TODO(acln): add a benchmark

## License

Pacakge zerocopy is distributed under a BSD-style license. Apart from
the work belonging to the author, package zerocopy adapts and copies
Go standard library code and tests. See the LICENSE file, as well as
the individual copyright headers in source files.
