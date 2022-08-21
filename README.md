# Scratch - A simple scratchpad

This is my fork of the [schollz/rwtxt](https://github.com/schollz/rwtxt).
[Original
Readme](https://github.com/schollz/rwtxt/blob/e8d16f3cce9855720d989ac74e0c077a0fb608ca/README.md)

## Differences

* [WIP] Cleaner codebase.
* Improved Markdown Syntax.
  * Features from Github Flavoured Markdown: Task List, Strikethrough, Linkify,
    Table, Emoji.
  * Footnote Support.
  * Wiki Link Support.
* Inline Syntax Highlighting.
* Uses SQLite FTS5 for Text Search.
* Removes SQL Dump. User must manually implement it using `sqlite3` Command.

## Installation

Scratch can directly be installed using the Go toolchain by running the
following command.

``` bash
go install argc.in/scratch@latest
```

The SQLite library mattn/go-sqlite3 supports different compilation strategies.
By default, Go will try to compile the the SQLite library using the available C
Compiler toolchain. On most linux systems that would be GLibC and GCC. This can
be configured by build and link flags described here.

I personally prefer a statically linked binary that can be compiled using Musl
and GCC. On Alpine Linux, it can be done by running the following command. On
other Linux distributions, the CC variable can be configured to point to the
Musl GCC binary. Also note the "-s -w" flags to strip off the symbol tables and
debugging information to reduce the size.

``` bash
CC=/usr/bin/x86_64-alpine-linux-musl-gcc go build --ldflags '-linkmode external -extldflags "-static" -s -w' -o scratch ./cmd/rwtxt/main.go
```

Alternatively, I also provide a Docker image through Github Packages with
precompiled binary.

docker run -it \
    -v /path/to/data:/data \
    -p 8152:8152 \
    ghcr.io/ankitrgadiya/scratch@master -db /data/scratch.db
    
## Backup

SQLite provides backup API that is available through its command-line tool. This
can be used to backup the database.

``` bash
sqlite3 /data/scratch.db ".backup /path/to/backup.db"
```
