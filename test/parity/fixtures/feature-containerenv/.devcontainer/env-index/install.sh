#!/bin/sh
set -e
# Only RESULT is a shell name; numeric names need not reach child processes.
printenv RESULT > /sample-index-env.txt
