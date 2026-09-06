#!/bin/bash
# usage: run.sh <crate-dir-under-poc> <binary> [args...]
crate="$1"; bin="$2"; shift 2
exec "$HOME/target/$crate/release/$bin" "$@"
