# Install Djinn on Windows, without administrator rights: the binary of the latest release, checked against the
# release's SHA-256 sums, in %LOCALAPPDATA%\Programs\djinn, added to your user PATH. When no binary fits, it falls
# back on `go install`: on Windows the window needs no CGO, so that build has it too.
#
#   irm https://github.com/Empowill/Djinn/releases/latest/download/install.ps1 | iex
#
# Settings, all optional, as environment variables:
#   DJINN_VERSION      a release tag, such as v0.1.0; the latest release by default
#   DJINN_INSTALL_DIR  where djinn.exe goes; %LOCALAPPDATA%\Programs\djinn by default
#   DJINN_ASSET        the archive to take, without .zip (djinn_windows_arm64); picked for this system by default
#   DJINN_RELEASES     where releases come from; https://github.com/Empowill/Djinn/releases by default

# A script block: `irm | iex` runs in your own session, so its variables stay inside, and an error never closes
# your window (no `exit`).
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # The progress bar slows Invoke-WebRequest down a lot.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $releases = if ($env:DJINN_RELEASES) { $env:DJINN_RELEASES.TrimEnd('/') } else { 'https://github.com/Empowill/Djinn/releases' }
    $version = if ($env:DJINN_VERSION) { $env:DJINN_VERSION } else { 'latest' }
    $dir = if ($env:DJINN_INSTALL_DIR) { $env:DJINN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\djinn' }
    $base = if ($version -eq 'latest') { "$releases/latest/download" } else { "$releases/download/$version" }
    $module = 'github.com/empowill/djinn/cmd/djinn'

    function Say($text) { Write-Host "djinn: $text" }

    function Get-Asset {
        if ($env:DJINN_ASSET) { return $env:DJINN_ASSET }
        # The system's architecture, not this PowerShell's: an x64 PowerShell runs on ARM64 Windows too.
        $arch = $env:PROCESSOR_ARCHITECTURE
        try { $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
        if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
        switch ($arch.ToUpperInvariant()) {
            { $_ -in 'X64', 'AMD64' } { return 'djinn_windows_amd64' }
            'ARM64' { return 'djinn_windows_arm64' }
            default { return $null }
        }
    }

    # Returns the path of a djinn.exe that was downloaded, checked against its sum and starts; $null when none fits.
    function Get-Binary($tmp) {
        $sums = Join-Path $tmp 'SHA256SUMS'
        try { Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sums } catch {
            Say "no release found at $base"
            return $null
        }
        $asset = Get-Asset
        if (-not $asset) {
            Say "no binary of this release fits this processor"
            return $null
        }
        $archive = "$asset.zip"
        $want = $null
        foreach ($line in Get-Content $sums) {
            $fields = $line -split '\s+'
            if ($fields.Count -ge 2 -and $fields[1] -eq $archive) { $want = $fields[0].ToLowerInvariant() }
        }
        if (-not $want) {
            Say "this release has no $archive"
            return $null
        }
        Say "downloading $archive"
        $zip = Join-Path $tmp $archive
        try { Invoke-WebRequest -UseBasicParsing -Uri "$base/$archive" -OutFile $zip } catch {
            Say "could not download $archive"
            return $null
        }
        $got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLowerInvariant()
        if ($got -ne $want) {
            throw "djinn: $archive does not match its SHA-256 sum: nothing was installed. Do not use this download."
        }
        Expand-Archive -Path $zip -DestinationPath $tmp -Force
        $exe = Join-Path $tmp "$asset\djinn.exe"
        # DJINN_HOME keeps this check away from any Djinn data.
        $saved = $env:DJINN_HOME
        $env:DJINN_HOME = Join-Path $tmp 'home'
        try { & $exe version | Out-Null; $ok = $LASTEXITCODE -eq 0 } catch { $ok = $false }
        finally { $env:DJINN_HOME = $saved }
        if (-not $ok) {
            Say "$asset does not start on this system"
            return $null
        }
        return $exe
    }

    # Puts exe in place of djinn.exe. Windows refuses to replace a running executable but lets it be renamed: a
    # Djinn that runs moves aside (removed by a later install) and keeps going.
    function Install-Binary($exe) {
        $target = Join-Path $dir 'djinn.exe'
        $new = Join-Path $dir '.djinn-new.exe'
        Copy-Item $exe $new -Force
        Get-ChildItem $dir -Filter '.swapexe-old-*' -ErrorAction SilentlyContinue |
            ForEach-Object { Remove-Item $_.FullName -Force -ErrorAction SilentlyContinue }
        if (Test-Path $target) {
            Rename-Item $target (Join-Path $dir ".swapexe-old-$([DateTime]::UtcNow.Ticks)-djinn.exe")
        }
        Rename-Item $new 'djinn.exe'
    }

    function Install-WithGo {
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
            throw "djinn: and Go is not installed. Install Go (https://go.dev/dl/), then run this again."
        }
        Say "installing with go install"
        $savedBin = $env:GOBIN
        $savedCgo = $env:CGO_ENABLED
        $env:GOBIN = $dir
        $env:CGO_ENABLED = '0'
        try {
            & go install "$module@$version"
            if ($LASTEXITCODE -ne 0) { throw "djinn: go install failed" }
        } finally {
            $env:GOBIN = $savedBin
            $env:CGO_ENABLED = $savedCgo
        }
    }

    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $dir = (Resolve-Path $dir).Path # go install wants an absolute GOBIN.
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("djinn-install-" + [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $exe = Get-Binary $tmp
        if ($exe) { Install-Binary $exe } else { Install-WithGo }
    } finally {
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    # On the user's PATH, for the next terminals and this one. No administrator: the user PATH only.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($userPath -split ';') -contains $dir)) {
        $joined = if ($userPath) { "$userPath;$dir" } else { $dir }
        [Environment]::SetEnvironmentVariable('Path', $joined, 'User')
        Say "added $dir to your PATH"
    }
    if (-not (($env:Path -split ';') -contains $dir)) { $env:Path = "$env:Path;$dir" }
    Say "installed in $dir. Start it with: djinn up"
}
