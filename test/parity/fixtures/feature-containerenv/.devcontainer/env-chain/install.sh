#!/bin/sh
set -e
{
  echo "PATH=$PATH"
  echo "ZZ_HOME=$ZZ_HOME"
  echo "AA_BIN=$AA_BIN"
  echo "MM_UNSET=$MM_UNSET"
  echo "QQ_DEFAULT=$QQ_DEFAULT"
  echo "QUOTED=$QUOTED"
} > /sample-install-env.txt
