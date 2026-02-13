#!/usr/bin/env pwsh
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ApiBase = if ([string]::IsNullOrWhiteSpace($env:API_BASE)) { "http://localhost:8080" } else { $env:API_BASE }
$DbService = if ([string]::IsNullOrWhiteSpace($env:DB_SERVICE)) { "db" } else { $env:DB_SERVICE }
$DbUser = if ([string]::IsNullOrWhiteSpace($env:DB_USER)) { "altpocket" } else { $env:DB_USER }
$DbName = if ([string]::IsNullOrWhiteSpace($env:DB_NAME)) { "altpocket" } else { $env:DB_NAME }
$JwtSecret = if ([string]::IsNullOrWhiteSpace($env:JWT_SECRET)) { "" } else { $env:JWT_SECRET }
$SmokePrefix = if ([string]::IsNullOrWhiteSpace($env:SMOKE_PREFIX)) { "smoke" } else { $env:SMOKE_PREFIX }

function Info([string]$Message) {
  [Console]::Error.WriteLine("[info] $Message")
}

function Escape-SqlLiteral([string]$Value) {
  return $Value.Replace("'", "''")
}

function ConvertTo-Base64Url([byte[]]$Bytes) {
  return [Convert]::ToBase64String($Bytes).TrimEnd("=").Replace("+", "-").Replace("/", "_")
}

$script:ComposeTokens = if ([string]::IsNullOrWhiteSpace($env:COMPOSE_CMD)) {
  @("docker", "compose")
} else {
  ($env:COMPOSE_CMD -split "\s+") | Where-Object { $_ -ne "" }
}

if ($script:ComposeTokens.Count -lt 1) {
  throw "COMPOSE_CMD is empty"
}

function Invoke-Compose([string[]]$Args) {
  $cmd = $script:ComposeTokens[0]
  $prefix = @()
  if ($script:ComposeTokens.Count -gt 1) {
    $prefix = $script:ComposeTokens[1..($script:ComposeTokens.Count - 1)]
  }

  $allArgs = @($prefix + $Args)
  $output = & $cmd @allArgs 2>&1
  if ($LASTEXITCODE -ne 0) {
    $outputText = ($output | Out-String).Trim()
    throw "compose command failed: $cmd $($allArgs -join ' ')`n$outputText"
  }

  return $output
}

function Derive-JwtSecret {
  $config = Invoke-Compose @("config")
  foreach ($line in $config) {
    if ($line -match '^\s+JWT_SECRET:\s*(.+)\s*$') {
      return $Matches[1].Trim('"')
    }
  }
  return "change-me"
}

function Invoke-PsqlScalar([string]$Sql) {
  $output = Invoke-Compose @(
    "exec", "-T", $DbService,
    "psql",
    "-U", $DbUser,
    "-d", $DbName,
    "-Atq",
    "-v", "ON_ERROR_STOP=1",
    "-c", $Sql
  )

  foreach ($line in $output) {
    $trimmed = $line.Trim()
    if ($trimmed.Length -gt 0) {
      return $trimmed
    }
  }

  return ""
}

function New-JwtHs256([string]$Secret, [string]$UserId) {
  $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
  $exp = $now + 86400
  $headerJson = '{"alg":"HS256","typ":"JWT"}'
  $payloadJson = ('{"sub":"{0}","iat":{1},"exp":{2}}' -f $UserId, $now, $exp)

  $headerB64 = ConvertTo-Base64Url ([Text.Encoding]::UTF8.GetBytes($headerJson))
  $payloadB64 = ConvertTo-Base64Url ([Text.Encoding]::UTF8.GetBytes($payloadJson))
  $unsignedToken = "$headerB64.$payloadB64"

  $hmac = [System.Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes($Secret))
  try {
    $signatureBytes = $hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes($unsignedToken))
  } finally {
    $hmac.Dispose()
  }

  $signatureB64 = ConvertTo-Base64Url $signatureBytes
  return "$unsignedToken.$signatureB64"
}

$health = Invoke-WebRequest -Uri "$ApiBase/healthz" -Method GET -SkipHttpErrorCheck
if ([int]$health.StatusCode -ne 200) {
  throw "API is not reachable at $ApiBase (status=$([int]$health.StatusCode))"
}

if ([string]::IsNullOrWhiteSpace($JwtSecret)) {
  $JwtSecret = Derive-JwtSecret
  Info "JWT secret was not provided; derived from compose config"
}

$nonce = "{0}-{1}" -f [DateTimeOffset]::UtcNow.ToUnixTimeSeconds(), ([Guid]::NewGuid().ToString("N"))
$googleSub = "{0}-sub-{1}" -f $SmokePrefix, $nonce
$email = "{0}+{1}@example.com" -f $SmokePrefix, $nonce
$name = "{0}-user-{1}" -f $SmokePrefix, $nonce
$avatar = "https://example.com/avatar.png"

$userIdSql = @"
INSERT INTO users (google_sub, email, name, avatar_url)
VALUES ('$(Escape-SqlLiteral $googleSub)', '$(Escape-SqlLiteral $email)', '$(Escape-SqlLiteral $name)', '$(Escape-SqlLiteral $avatar)')
RETURNING id;
"@
$userId = Invoke-PsqlScalar $userIdSql
if ([string]::IsNullOrWhiteSpace($userId)) {
  throw "failed to create smoke test user"
}

$csrfBytes = New-Object byte[] 24
[System.Security.Cryptography.RandomNumberGenerator]::Fill($csrfBytes)
$csrfToken = -join ($csrfBytes | ForEach-Object { $_.ToString("x2") })

$sessionSql = @"
INSERT INTO sessions (user_id, csrf_token, expires_at)
VALUES ('$(Escape-SqlLiteral $userId)', '$(Escape-SqlLiteral $csrfToken)', NOW() + interval '7 days')
RETURNING id;
"@
$sessionId = Invoke-PsqlScalar $sessionSql
if ([string]::IsNullOrWhiteSpace($sessionId)) {
  throw "failed to create smoke test session"
}

$jwt = New-JwtHs256 -Secret $JwtSecret -UserId $userId

[ordered]@{
  SMOKE_USER_ID        = $userId
  SMOKE_CSRF_TOKEN     = $csrfToken
  SMOKE_SESSION_ID     = $sessionId
  SMOKE_SESSION_COOKIE = "altpocket_session=$sessionId"
  SMOKE_JWT_TOKEN      = $jwt
} | ConvertTo-Json -Compress
