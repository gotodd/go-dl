## Straggler Detection and Fix

### What is a straggler
A straggler is one go-routine is takes much longer to download its part of file
than the rest of the go-routines. More specificially, if a go-routine takes 20
seconds and 50% longer than the average of finished go-routines, which has to
be more than 80% of all go-routines, it's a straggler.

### What to do when a go-routine is detected as being a straggler
When detected as a straggler, that go-routine will be cancelled via the cancel
context and another go-routine will be started to resume the download
