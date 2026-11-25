`attn seed check report` no longer destroys files: evidence replaces a file on the seed only when an earlier report on the same check cited that file. Any other evidence that would change a file on the seed is refused, naming the file and the fix, and nothing is copied. Evidence identical to the seed's file cites it without a copy. With that, the command is a `record`.

The seed document gains a field for what earlier reports cited, so a migration keeps older builds off the database.
