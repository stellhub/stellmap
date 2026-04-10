$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$protoRoot = Join-Path $repoRoot "api\proto"
$outDir = Join-Path $repoRoot "api\gen\go"

New-Item -ItemType Directory -Force -Path $outDir | Out-Null

protoc `
  -I $protoRoot `
  --go_out=$outDir `
  --go_opt=paths=source_relative `
  --go-grpc_out=$outDir `
  --go-grpc_opt=paths=source_relative,require_unimplemented_servers=false `
  (Join-Path $protoRoot "starmap\v1\raft.proto") `
  (Join-Path $protoRoot "starmap\v1\snapshot.proto")
