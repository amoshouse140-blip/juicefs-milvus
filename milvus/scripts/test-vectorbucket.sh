#!/usr/bin/env bash
set -euo pipefail

base_url="${1:-http://127.0.0.1:9000}"

curl -sS -X POST "${base_url}/CreateVectorBucket" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket"}'
echo

curl -sS -X POST "${base_url}/CreateIndex" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","dataType":"float32","dimension":4,"distanceMetric":"cosine"}'
echo

curl -sS -X POST "${base_url}/PutVectors" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","vectors":[{"key":"v1","data":{"float32":[0.1,0.2,0.3,0.4]},"metadata":{"tenant":"t1"}}]}'
echo

curl -sS -X POST "${base_url}/QueryVectors" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","queryVector":{"float32":[0.1,0.2,0.3,0.4]},"topK":5,"returnDistance":true,"returnMetadata":true}'
echo

curl -sS -X POST "${base_url}/DeleteVectors" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index","keys":["v1"]}'
echo

curl -sS -X POST "${base_url}/DeleteIndex" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket","indexName":"demo-index"}'
echo

curl -sS -X POST "${base_url}/DeleteVectorBucket" \
  -H 'Content-Type: application/json' \
  -H 'X-Amz-Account-Id: 123456789012' \
  -d '{"vectorBucketName":"demo-bucket"}'
echo
