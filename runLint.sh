#!/bin/bash

export GOPATH="$HOME/go"
export PATH="$PATH:$GOPATH/bin"
make tools
make lint
