# Instance registry

`Join(dataDir, info)` takes one of eight slots and returns a membership and the other live instances.
Keep the membership until shutdown, use `Others()` to refresh the list, and call `Close()` to release its slot.
An error from `Join` leaves no slot held, including when all eight slots are occupied.

The registry uses `instances/guard.lock` and `instances/0.lock` through `instances/7.lock` under the data directory.
It creates the guard file on its first scan and a slot file only when an instance claims that slot.
It never deletes these files.
Each slot contains JSON with `url`, `pid` and `started` (an RFC3339 timestamp).
The file name supplies `Info.Slot`.
A slot's contents identify a live instance only while its owner holds the slot lock.
Free slots can contain stale or incomplete JSON.

`Join` and `Others` hold an exclusive guard lock while they scan the slots.
This keeps temporary probes of free slots and incomplete writes out of the results.
Linux uses `flock`.
Windows uses `LockFileEx` on one byte at offset 2^32, beyond the JSON, so another handle can read an occupied slot.
The operating system releases the locks when the process ends.

Run the tests with `nix develop -c go test ./internal/instance`.
The same tests run on Windows after cross-compilation with `GOOS=windows go test -c ./internal/instance`.
