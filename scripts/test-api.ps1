#!/usr/bin/env pwsh
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ApiBase = if ([string]::IsNullOrWhiteSpace($env:API_BASE)) { "http://localhost:8080" } else { $env:API_BASE }
$CredentialScript = if ([string]::IsNullOrWhiteSpace($env:CREDENTIAL_SCRIPT)) { (Join-Path $PSScriptRoot "get-test-credentials.ps1") } else { $env:CREDENTIAL_SCRIPT }
$RunRateLimitTest = if ([string]::IsNullOrWhiteSpace($env:RUN_RATE_LIMIT_TEST)) { 1 } else { [int]$env:RUN_RATE_LIMIT_TEST }
$KeepTestData = if ([string]::IsNullOrWhiteSpace($env:KEEP_TEST_DATA)) { 0 } else { [int]$env:KEEP_TEST_DATA }
$DbService = if ([string]::IsNullOrWhiteSpace($env:DB_SERVICE)) { "db" } else { $env:DB_SERVICE }
$DbUser = if ([string]::IsNullOrWhiteSpace($env:DB_USER)) { "" } else { $env:DB_USER }
$DbName = if ([string]::IsNullOrWhiteSpace($env:DB_NAME)) { "" } else { $env:DB_NAME }
$script:DbUserResolved = if ([string]::IsNullOrWhiteSpace($DbUser)) { "altpocket" } else { $DbUser }
$script:DbNameResolved = if ([string]::IsNullOrWhiteSpace($DbName)) { "altpocket" } else { $DbName }

$script:ComposeTokens = if ([string]::IsNullOrWhiteSpace($env:COMPOSE_CMD)) {
  @("docker", "compose")
} else {
  ($env:COMPOSE_CMD -split "\s+") | Where-Object { $_ -ne "" }
}

if ($script:ComposeTokens.Count -lt 1) {
  throw "COMPOSE_CMD is empty"
}

function Get-CurlCommand {
  $curlExe = Get-Command curl.exe -ErrorAction SilentlyContinue
  if ($null -ne $curlExe) {
    return $curlExe.Path
  }

  $curl = Get-Command curl -ErrorAction SilentlyContinue
  if ($null -ne $curl) {
    return $curl.Path
  }

  throw "curl command not found"
}

$script:CurlCommand = Get-CurlCommand
$script:TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("altpocket-smoke-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $script:TmpDir | Out-Null
$script:TmpBody = Join-Path $script:TmpDir "body"
$script:TmpHeaders = Join-Path $script:TmpDir "headers"

$script:LastBody = ""
$script:LastHeadersText = ""
$script:SmokeUserId = ""
$script:SmokeCsrfToken = ""
$script:SmokeSessionCookie = ""
$script:SmokeJwtToken = ""

function Info([string]$Message) {
  [Console]::WriteLine("[info] $Message")
}

function Escape-SqlLiteral([string]$Value) {
  return $Value.Replace("'", "''")
}

function Invoke-Compose {
  param(
    [string[]]$Args,
    [switch]$IgnoreError
  )

  $cmd = $script:ComposeTokens[0]
  $prefix = @()
  if ($script:ComposeTokens.Count -gt 1) {
    $prefix = $script:ComposeTokens[1..($script:ComposeTokens.Count - 1)]
  }

  $allArgs = @($prefix + $Args)
  $output = & $cmd @allArgs 2>&1
  if ((-not $IgnoreError) -and $LASTEXITCODE -ne 0) {
    $outputText = ($output | Out-String).Trim()
    throw "compose command failed: $cmd $($allArgs -join ' ')`n$outputText"
  }

  return $output
}

function Cleanup {
  if (Test-Path $script:TmpDir) {
    Remove-Item -Recurse -Force $script:TmpDir
  }

  if ($KeepTestData -eq 1) {
    return
  }

  if ([string]::IsNullOrWhiteSpace($script:SmokeUserId)) {
    return
  }

  $cleanupSql = "DELETE FROM users WHERE id='$(Escape-SqlLiteral $script:SmokeUserId)'; DELETE FROM tags t WHERE NOT EXISTS (SELECT 1 FROM item_tags it WHERE it.tag_id=t.id);"
  Invoke-Compose -IgnoreError -Args @(
    "exec", "-T", $DbService,
    "psql",
    "-U", $script:DbUserResolved,
    "-d", $script:DbNameResolved,
    "-c", $cleanupSql
  ) | Out-Null
}

function Fail([string]$Message) {
  if (-not [string]::IsNullOrWhiteSpace($script:LastBody)) {
    throw "$Message`nresponse body: $($script:LastBody.Trim())"
  }
  throw $Message
}

function Invoke-ApiRequest {
  param(
    [string]$Method,
    [string]$Url,
    [string]$Data = "",
    [string[]]$Headers = @()
  )

  $args = @(
    "-sS",
    "-o", $script:TmpBody,
    "-D", $script:TmpHeaders,
    "-w", "%{http_code}",
    "-X", $Method,
    $Url
  )

  if (-not [string]::IsNullOrWhiteSpace($Data)) {
    $args += @("-H", "Content-Type: application/json", "--data", $Data)
  }

  foreach ($header in $Headers) {
    $args += @("-H", $header)
  }

  $statusOutput = & $script:CurlCommand @args
  if ($LASTEXITCODE -ne 0) {
    throw "curl request failed: $Method $Url"
  }

  $script:LastBody = if (Test-Path $script:TmpBody) { Get-Content -Raw -Path $script:TmpBody } else { "" }
  $script:LastHeadersText = if (Test-Path $script:TmpHeaders) { Get-Content -Raw -Path $script:TmpHeaders } else { "" }

  return ($statusOutput | Out-String).Trim()
}

function Assert-Status([string]$Actual, [string]$Expected, [string]$Label) {
  if ($Actual -ne $Expected) {
    Fail ("{0}: expected {1}, got {2}" -f $Label, $Expected, $Actual)
  }
}

function Assert-BodyContains([string]$Needle, [string]$Label) {
  if (-not $script:LastBody.Contains($Needle)) {
    Fail ("{0}: expected body to contain '{1}'" -f $Label, $Needle)
  }
}

function Assert-HeaderContains([string]$Needle, [string]$Label) {
  if (-not $script:LastHeadersText.ToLowerInvariant().Contains($Needle.ToLowerInvariant())) {
    Fail ("{0}: expected headers to contain '{1}'" -f $Label, $Needle)
  }
}

function Get-JsonStringField([string]$Field) {
  $pattern = '"' + [Regex]::Escape($Field) + '":"([^"]+)"'
  if ($script:LastBody -match $pattern) {
    return $Matches[1]
  }
  return ""
}

function Wait-ForApi {
  Info "Waiting for API at $ApiBase/healthz"
  for ($i = 0; $i -lt 30; $i++) {
    try {
      $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/healthz"
      if ($status -eq "200" -and $script:LastBody.Trim() -eq "ok") {
        Info "API is ready"
        return
      }
    } catch {
      # Retry until timeout.
    }
    Start-Sleep -Seconds 1
  }
  Fail "API did not become ready within 30s"
}

function Provision-Credentials {
  Info "Provisioning smoke test credentials"
  if (-not (Test-Path $CredentialScript)) {
    Fail "credential script is missing: $CredentialScript"
  }

  $credentialOutput = & $CredentialScript
  if ($LASTEXITCODE -ne 0) {
    throw "credential script failed: $CredentialScript"
  }

  $credentialJson = ($credentialOutput | Out-String).Trim()
  if ([string]::IsNullOrWhiteSpace($credentialJson)) {
    Fail "credential script returned empty output"
  }

  $credentials = $credentialJson | ConvertFrom-Json
  $script:SmokeUserId = [string]$credentials.SMOKE_USER_ID
  $script:SmokeCsrfToken = [string]$credentials.SMOKE_CSRF_TOKEN
  $script:SmokeSessionCookie = [string]$credentials.SMOKE_SESSION_COOKIE
  $script:SmokeJwtToken = [string]$credentials.SMOKE_JWT_TOKEN
  $credentialDbUser = [string]$credentials.SMOKE_DB_USER
  $credentialDbName = [string]$credentials.SMOKE_DB_NAME

  if ([string]::IsNullOrWhiteSpace($script:SmokeUserId)) { Fail "SMOKE_USER_ID is empty" }
  if ([string]::IsNullOrWhiteSpace($script:SmokeCsrfToken)) { Fail "SMOKE_CSRF_TOKEN is empty" }
  if ([string]::IsNullOrWhiteSpace($script:SmokeSessionCookie)) { Fail "SMOKE_SESSION_COOKIE is empty" }
  if ([string]::IsNullOrWhiteSpace($script:SmokeJwtToken)) { Fail "SMOKE_JWT_TOKEN is empty" }

  if (-not [string]::IsNullOrWhiteSpace($credentialDbUser)) {
    $script:DbUserResolved = $credentialDbUser
  }
  if (-not [string]::IsNullOrWhiteSpace($credentialDbName)) {
    $script:DbNameResolved = $credentialDbName
  }
}

function New-TestUrl([string]$Prefix) {
  $epoch = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
  $suffix = ([Guid]::NewGuid().ToString("N")).Substring(0, 8)
  return "https://example.com/$Prefix/$epoch/$suffix"
}

function Run-RateLimitTest {
  Info "Rate limit test (POST /v1/items, expect 429 after burst)"
  $hit429 = $false
  for ($i = 1; $i -le 60; $i++) {
    $url = New-TestUrl "rate-limit-$i"
    $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/items" -Data "{""url"":""$url"",""tags"":[""rate""]}" -Headers @("Authorization: Bearer $script:SmokeJwtToken")
    if ($status -eq "429") {
      $hit429 = $true
      break
    }
    if ($status -ne "200") {
      Fail "rate limit test: expected 200/429, got $status"
    }
  }

  if (-not $hit429) {
    Fail "rate limit test: expected at least one 429"
  }
}

function Main {
  Wait-ForApi

  Info "GET /healthz should return 200 and body 'ok'"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/healthz"
  Assert-Status $status "200" "GET /healthz"
  if ($script:LastBody.Trim() -ne "ok") { Fail "GET /healthz body mismatch" }

  Info "OPTIONS /v1/items should return CORS preflight 204"
  $status = Invoke-ApiRequest -Method "OPTIONS" -Url "$ApiBase/v1/items" -Headers @("Origin: http://localhost:3000", "Access-Control-Request-Method: POST")
  Assert-Status $status "204" "OPTIONS /v1/items"
  Assert-HeaderContains "Access-Control-Allow-Origin: *" "OPTIONS /v1/items"

  Info "GET /v1/auth/google/login should redirect and set oauth_state"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/v1/auth/google/login"
  Assert-Status $status "302" "GET /v1/auth/google/login"
  Assert-HeaderContains "Location:" "GET /v1/auth/google/login"
  Assert-HeaderContains "Set-Cookie: oauth_state=" "GET /v1/auth/google/login"

  Info "GET /v1/auth/google/callback without valid state should return 400"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/v1/auth/google/callback"
  Assert-Status $status "400" "GET /v1/auth/google/callback"

  Info "POST /v1/auth/extension/exchange with invalid payload should return 400"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/auth/extension/exchange" -Data "{}"
  Assert-Status $status "400" "POST /v1/auth/extension/exchange invalid payload"

  Info "POST /v1/auth/extension/exchange with invalid token should return 401"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/auth/extension/exchange" -Data '{"id_token":"invalid"}'
  Assert-Status $status "401" "POST /v1/auth/extension/exchange invalid token"

  Info "GET /v1/items without auth should return 401"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/v1/items"
  Assert-Status $status "401" "GET /v1/items without auth"

  Info "POST /v1/items without auth should return 403 (csrf)"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/items" -Data '{"url":"https://example.com"}'
  Assert-Status $status "403" "POST /v1/items without auth"

  Provision-Credentials

  $sessionHeaders = @(
    "Cookie: $script:SmokeSessionCookie",
    "X-CSRF-Token: $script:SmokeCsrfToken"
  )

  $itemUrl = New-TestUrl "session-create"
  Info "POST /v1/items with session+csrf should create item"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/items" -Data "{""url"":""$itemUrl"",""tags"":[""Go"",""Backend""]}" -Headers $sessionHeaders
  Assert-Status $status "200" "POST /v1/items with session"
  Assert-BodyContains '"created":true' "POST /v1/items with session"
  $itemId = Get-JsonStringField "item_id"
  if ([string]::IsNullOrWhiteSpace($itemId)) { Fail "POST /v1/items did not return item_id" }

  Info "GET /v1/items with session should include created item"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/v1/items?sort=newest" -Headers @("Cookie: $script:SmokeSessionCookie")
  Assert-Status $status "200" "GET /v1/items with session"
  Assert-BodyContains '"items":' "GET /v1/items with session"
  Assert-BodyContains '"pagination":' "GET /v1/items with session"
  Assert-BodyContains '"per_page":' "GET /v1/items with session"
  Assert-BodyContains '"user_id":"' "GET /v1/items with session"
  Assert-BodyContains $itemId "GET /v1/items with session"

  Info "GET /v1/items/{id} with session should return item"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/v1/items/$itemId" -Headers @("Cookie: $script:SmokeSessionCookie")
  Assert-Status $status "200" "GET /v1/items/{id} with session"
  Assert-BodyContains '"id":"' "GET /v1/items/{id} with session"
  Assert-BodyContains '"canonical_url":"' "GET /v1/items/{id} with session"
  Assert-BodyContains '"content_full":' "GET /v1/items/{id} with session"
  Assert-BodyContains '"tags":' "GET /v1/items/{id} with session"
  Assert-BodyContains $itemId "GET /v1/items/{id} with session"

  Info "GET /v1/tags?q=go with session should return normalized tag"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/v1/tags?q=go" -Headers @("Cookie: $script:SmokeSessionCookie")
  Assert-Status $status "200" "GET /v1/tags with session"
  Assert-BodyContains '"name":"go"' "GET /v1/tags with session"
  Assert-BodyContains '"normalized_name":"go"' "GET /v1/tags with session"

  Info "POST /v1/items/{id}/refetch with session+csrf should return 202"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/items/$itemId/refetch" -Headers $sessionHeaders
  Assert-Status $status "202" "POST /v1/items/{id}/refetch with session"
  Assert-BodyContains '"status":"queued"' "POST /v1/items/{id}/refetch with session"

  Info "DELETE /v1/items/{id} with session+csrf should return 204"
  $status = Invoke-ApiRequest -Method "DELETE" -Url "$ApiBase/v1/items/$itemId" -Headers $sessionHeaders
  Assert-Status $status "204" "DELETE /v1/items/{id} with session"

  Info "GET /v1/items/{id} after delete should return 404"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/v1/items/$itemId" -Headers @("Cookie: $script:SmokeSessionCookie")
  Assert-Status $status "404" "GET /v1/items/{id} after delete"

  $bearerItemUrl = New-TestUrl "bearer-create"
  Info "POST /v1/items with bearer token should create item"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/items" -Data "{""url"":""$bearerItemUrl"",""tags"":[""Api"",""Smoke""]}" -Headers @("Authorization: Bearer $script:SmokeJwtToken")
  Assert-Status $status "200" "POST /v1/items with bearer"
  Assert-BodyContains '"created":true' "POST /v1/items with bearer"
  $bearerItemId = Get-JsonStringField "item_id"
  if ([string]::IsNullOrWhiteSpace($bearerItemId)) { Fail "POST /v1/items with bearer did not return item_id" }

  Info "POST /v1/items/{id}/refetch with bearer should return 202"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/items/$bearerItemId/refetch" -Headers @("Authorization: Bearer $script:SmokeJwtToken")
  Assert-Status $status "202" "POST /v1/items/{id}/refetch with bearer"

  Info "DELETE /v1/items/{id} with bearer should return 204"
  $status = Invoke-ApiRequest -Method "DELETE" -Url "$ApiBase/v1/items/$bearerItemId" -Headers @("Authorization: Bearer $script:SmokeJwtToken")
  Assert-Status $status "204" "DELETE /v1/items/{id} with bearer"

  Info "POST /v1/items with session but missing csrf should return 403"
  $csrfMissingUrl = New-TestUrl "csrf-missing"
  $status = Invoke-ApiRequest -Method "POST" -Url "$ApiBase/v1/items" -Data "{""url"":""$csrfMissingUrl""}" -Headers @("Cookie: $script:SmokeSessionCookie")
  Assert-Status $status "403" "POST /v1/items with missing csrf"

  Info "GET /ui/items with session should return 200"
  $status = Invoke-ApiRequest -Method "GET" -Url "$ApiBase/ui/items" -Headers @("Cookie: $script:SmokeSessionCookie")
  Assert-Status $status "200" "GET /ui/items with session"
  Assert-BodyContains "<!DOCTYPE html>" "GET /ui/items with session"

  if ($RunRateLimitTest -eq 1) {
    Run-RateLimitTest
  }

  Info "All smoke tests passed"
}

try {
  Main
} catch {
  [Console]::Error.WriteLine("[fail] $($_.Exception.Message)")
  exit 1
} finally {
  Cleanup
}
