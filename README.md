Jobliterator
============

Super simple binary that deletes jobs older than `-days` days (default 7 days).

If `-f` is not specified it will only list jobs eligible for deletion.

## Usage:

Outside of Kubernetes cluster:
`./jobliterator -kubeconfig ~/.kube/config -context prod-cluster -f` 

Inside Kubernetes cluster:
`./jobliterator -in-cluster -context prod-cluster -f`

## Examples

CronJob and one-off Jobs are in `manifests`.

Simple Dockerfile in `docker`.

