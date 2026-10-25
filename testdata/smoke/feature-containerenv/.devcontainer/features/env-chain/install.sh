#!/bin/sh
set -e
{
  echo "AA_BIN=$AA_BIN"
  echo "MM_UNSET=$MM_UNSET"
  echo "QQ_DEFAULT=$QQ_DEFAULT"
  echo "PATH=$PATH"
  echo "go=$(command -v go)"
} > /usr/local/share/dcb-env-chain.txt
