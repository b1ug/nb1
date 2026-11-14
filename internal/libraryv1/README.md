# Local library v1

The library stores named exact RGB colors and frozen host Sequences. A Pattern has an ID, an optional title of up to 256 Unicode code points, and its normalized Sequence. Saving and reading enforce the title limit.

`New`, list and lookup operations leave a missing library directory absent. Saves create private directories and write a temporary 0600 record before atomic replacement. macOS and Linux directories use 0700. Windows uses inherited directory ACLs and replacement through `MoveFileEx`.

The dedicated root contains `colors` and `patterns` directories. Record filenames hex-encode the original name or ID so case-distinct Pattern IDs work on case-insensitive filesystems. Each record is bounded to 1 MiB. Limits are 256 colors and 128 Patterns.

Cooperating writers share the existing OS ownership lock while checking capacity and replacing a record. Lock acquisition waits up to two seconds. Readers open complete regular files with platform symlink guards. A saved Sequence is independent of later caller edits.
