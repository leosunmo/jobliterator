Jobliterator
============

Super simple binary that deletes jobs older than `-days` days (default 7 days).

If `-delete` is not specified it will only list jobs eligible for deletion.

Usage:

`./jobliterator -kubeconfig ~/.kube/config -context prod-cluster -delete` 

TODO:

Gotta get some error handling for the delete command.