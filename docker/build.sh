#!/bin/bash

if [ -z ${1+x} ]; then echo "Usage: ./build.sh \$tag"; exit; fi

CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o jobliterator ../.

sudo docker build -t jobliterator:$1 .
