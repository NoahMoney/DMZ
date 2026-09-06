#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
# Runs TLC on VeilWrap.tla inside WSL (OpenJDK 21). Downloads tla2tools.jar once into ~/tla.
set -e
mkdir -p "$HOME/tla"
if [ ! -f "$HOME/tla/tla2tools.jar" ]; then
  curl -sSL -o "$HOME/tla/tla2tools.jar" https://github.com/tlaplus/tlaplus/releases/latest/download/tla2tools.jar
fi
cd $REPO/research/wrapper/formal
java -XX:+UseParallelGC -Xmx4g -cp "$HOME/tla/tla2tools.jar" tlc2.TLC -workers 8 -deadlock -config VeilWrap.cfg VeilWrap.tla 2>&1 | tee tlc-output.txt | tail -40
