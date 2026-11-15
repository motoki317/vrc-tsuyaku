# Instance registry

`Join(dataDir, info)` takes one of eight slots. When all eight are held, it returns an error.
Keep the membership that it returns until shutdown.

The registry uses `instances/guard.lock` and `instances/0.lock` through `instances/7.lock` under the data directory.
It creates the guard file on its first scan and a slot file only when an instance claims that slot.
It never deletes these files.
A slot file contains JSON with `url`, `pid`, `host` and `started` (an RFC3339 timestamp).
`host` is the owner's host name: on a shared folder, a lock can belong to a process on another machine, where `pid` and `url` name something else.
The JSON identifies a live instance only while its owner holds the file's lock.
A free slot's file can be missing, or can contain stale or incomplete JSON.

`Join` and `Others` hold an exclusive guard lock while they scan the slots.
A scan tests each slot by trying to lock it, and a claim writes its JSON after it locks the slot. The guard keeps another scan from reading either as a live instance.
The operating system releases the locks when the process ends.

The lock code differs between Linux and Windows. To run the tests on Windows, cross-compile them with `GOOS=windows go test -c ./internal/instance` and run the binary there.
