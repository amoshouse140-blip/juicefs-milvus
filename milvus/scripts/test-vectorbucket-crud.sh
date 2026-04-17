#!/usr/bin/env bash
set -euo pipefail

base_url="${1:-http://127.0.0.1:9000}"
account_id="${ACCOUNT_ID:-123456789012}"
bucket_name="${BUCKET_NAME:-demo-bucket-crud}"
index_name="${INDEX_NAME:-demo-index}"

body_file="$(mktemp)"
status_file="$(mktemp)"
trap 'rm -f "${body_file}" "${status_file}"' EXIT

request() {
  local name="$1"
  local payload="$2"
  curl -sS \
    -o "${body_file}" \
    -w "%{http_code}" \
    -X POST "${base_url}/${name}" \
    -H 'Content-Type: application/json' \
    -H "X-Amz-Account-Id: ${account_id}" \
    -d "${payload}" > "${status_file}"
}

expect_http_2xx() {
  local api="$1"
  local status
  status="$(cat "${status_file}")"
  if [[ ! "${status}" =~ ^2 ]]; then
    echo "${api} failed with HTTP ${status}" >&2
    cat "${body_file}" >&2
    exit 1
  fi
}

json_get() {
  local expr="$1"
  python3 -c 'import json,sys; data=json.load(open(sys.argv[1])); value=data
for key in sys.argv[2].split("."):
    if key:
        value=value[key]
if isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)' "${body_file}" "${expr}"
}

json_eval() {
  local code="$1"
  python3 -c "import json,sys; data=json.load(open(sys.argv[1])); ${code}" "${body_file}"
}

cleanup() {
  request "DeleteIndex" "{\"vectorBucketName\":\"${bucket_name}\",\"indexName\":\"${index_name}\"}" >/dev/null 2>&1 || true
  request "DeleteVectorBucket" "{\"vectorBucketName\":\"${bucket_name}\"}" >/dev/null 2>&1 || true
}

trap_cleanup() {
  cleanup || true
}

trap trap_cleanup EXIT

echo "1. CreateVectorBucket"
request "CreateVectorBucket" "{\"vectorBucketName\":\"${bucket_name}\"}"
expect_http_2xx "CreateVectorBucket"
bucket_arn="$(json_get "vectorBucketArn")"
echo "   bucketArn=${bucket_arn}"

echo "2. CreateIndex"
request "CreateIndex" "{\"vectorBucketName\":\"${bucket_name}\",\"indexName\":\"${index_name}\",\"dataType\":\"float32\",\"dimension\":4,\"distanceMetric\":\"cosine\"}"
expect_http_2xx "CreateIndex"
index_arn="$(json_get "indexArn")"
echo "   indexArn=${index_arn}"

echo "3. PutVectors"
request "PutVectors" "$(cat <<JSON
{"vectorBucketName":"${bucket_name}","indexName":"${index_name}","vectors":[
  {"key":"v1","data":{"float32":[0.10,0.20,0.30,0.40]},"metadata":{"tenant":"t1","color":"red"}},
  {"key":"v2","data":{"float32":[0.91,0.81,0.71,0.61]},"metadata":{"tenant":"t2","color":"blue"}}
]}
JSON
)"
expect_http_2xx "PutVectors"
echo "   inserted=v1,v2"

echo "4. QueryVectors before delete"
request "QueryVectors" "$(cat <<JSON
{"vectorBucketName":"${bucket_name}","indexName":"${index_name}","queryVector":{"float32":[0.10,0.20,0.30,0.40]},"topK":2,"returnDistance":true,"returnMetadata":true}
JSON
)"
expect_http_2xx "QueryVectors(before delete)"
json_eval '
vectors = data.get("vectors", [])
assert len(vectors) >= 1, "expected at least one query result"
assert vectors[0]["key"] == "v1", f"expected top result v1, got {vectors[0].get(\"key\")}"
metadata = vectors[0].get("metadata", {})
assert metadata.get("tenant") == "t1", f"expected tenant t1, got {metadata}"
print("   topResult=", vectors[0]["key"], "distance=", vectors[0].get("distance"))
'

echo "5. DeleteVectors"
request "DeleteVectors" "{\"vectorBucketName\":\"${bucket_name}\",\"indexName\":\"${index_name}\",\"keys\":[\"v1\"]}"
expect_http_2xx "DeleteVectors"
echo "   deleted=v1"

echo "6. QueryVectors after delete"
request "QueryVectors" "$(cat <<JSON
{"vectorBucketName":"${bucket_name}","indexName":"${index_name}","queryVector":{"float32":[0.10,0.20,0.30,0.40]},"topK":2,"returnDistance":true,"returnMetadata":true}
JSON
)"
expect_http_2xx "QueryVectors(after delete)"
json_eval '
vectors = data.get("vectors", [])
keys = [item.get("key") for item in vectors]
assert "v1" not in keys, f"expected v1 to be deleted, got {keys}"
print("   remainingKeys=", keys)
'

echo "7. Cleanup"
request "DeleteIndex" "{\"vectorBucketName\":\"${bucket_name}\",\"indexName\":\"${index_name}\"}"
expect_http_2xx "DeleteIndex"
request "DeleteVectorBucket" "{\"vectorBucketName\":\"${bucket_name}\"}"
expect_http_2xx "DeleteVectorBucket"
trap - EXIT

echo
echo "VectorBucket CRUD test passed."
