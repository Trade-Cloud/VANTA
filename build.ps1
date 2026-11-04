# =====================================================================
#  VANTA — сборка для Windows
#
#  .\build.ps1                     сборка под текущую архитектуру
#  .\build.ps1 -Arch arm64         amd64 | arm64
#  .\build.ps1 -Debugger           дополнительно собрать debugger\
#  .\build.ps1 -OnlyDebugger       собрать только debugger\
#  .\build.ps1 -Clean              удалить dist\ перед сборкой
#  .\build.ps1 -NoIcon             без иконки/манифеста в .exe (без go-winres)
#
#  Или двойным кликом / из cmd: build.bat (те же аргументы).
#
#  Результат: dist\windows-<arch>\Vanta.exe
#  Консоль НЕ открывается: линковка с -H=windowsgui (GUI-подсистема).
#  Иконка и манифест (DPI-aware) вшиваются через go-winres (.syso).
#
#  Требования: Go и C-компилятор для cgo (Fyne):
#    MSYS2 -> pacman -S mingw-w64-ucrt-x86_64-gcc, добавить ...\ucrt64\bin в PATH
#    (или TDM-GCC / llvm-mingw для arm64).
# =====================================================================
[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = '',
    [switch]$Debugger,
    [switch]$OnlyDebugger,
    [switch]$Clean,
    [switch]$NoIcon
)

$ErrorActionPreference = 'Stop'
$Root = $PSScriptRoot
Set-Location $Root

$WinresVersion = 'v0.3.3'   # github.com/tc-hib/go-winres

function Log([string]$m) { Write-Host "> $m" -ForegroundColor DarkYellow }
function Ok([string]$m)  { Write-Host "OK $m" -ForegroundColor Green }
function Fail([string]$m) { Write-Host "X $m" -ForegroundColor Red; exit 1 }

# ---------- метаданные из FyneApp.toml ----------
function TomlGet([string]$key, [string]$def) {
    $line = Get-Content (Join-Path $Root 'FyneApp.toml') |
        Where-Object { $_ -match "^\s*$key\s*=" } | Select-Object -First 1
    if (-not $line) { return $def }
    return (($line -split '=', 2)[1]).Trim().Trim('"')
}
$AppName    = TomlGet 'Name' 'Vanta'
$AppVersion = TomlGet 'Version' '0.1.0'
$AppBuild   = TomlGet 'Build' '1'
$IconPng    = Join-Path $Root 'assets\app-icon\mac-1024.png'

# Версия для ресурсов Windows: строго 4 числа (0.1.0.1)
$verParts = @($AppVersion -split '[^0-9]+' | Where-Object { $_ -ne '' })
while ($verParts.Count -lt 3) { $verParts += '0' }
$FileVersion = ($verParts[0..2] + $AppBuild) -join '.'

# ---------- окружение ----------
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail 'Go не найден в PATH' }

$gcc = $env:CC
if (-not $gcc) { $gcc = 'gcc' }
if (-not (Get-Command ($gcc -split ' ')[0] -ErrorAction SilentlyContinue)) {
    Fail "C-компилятор '$gcc' не найден. Fyne требует cgo: установите MSYS2 (mingw-w64 gcc) и добавьте его bin в PATH."
}

if (-not $Arch) { $Arch = (& go env GOARCH).Trim() }
$Dist = Join-Path $Root "dist\windows-$Arch"

if ($Clean -and (Test-Path (Join-Path $Root 'dist'))) {
    Log 'Удаляю dist\'
    Remove-Item -Recurse -Force (Join-Path $Root 'dist')
}
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

$env:CGO_ENABLED = '1'

# -H=windowsgui — главное: exe собирается под GUI-подсистему, окно консоли не создаётся.
$LdFlags = '-s -w -H=windowsgui'

# ---------- ресурсы .exe: иконка + манифест ----------
function New-Resources([string]$pkgDir, [string]$name, [string]$desc) {
    if ($NoIcon) { return }
    Log "go-winres: иконка и манифест для $name"
    Push-Location $pkgDir
    try {
        & go run "github.com/tc-hib/go-winres@$WinresVersion" simply `
            --arch $Arch `
            --out 'zz_vanta_rsrc' `
            --manifest gui `
            --icon $IconPng `
            --product-name $AppName `
            --product-version $FileVersion `
            --file-version $FileVersion `
            --file-description $desc `
            --original-filename "$name.exe" `
            --copyright "Trade-Cloud"
        if ($LASTEXITCODE -ne 0) { throw 'go-winres завершился с ошибкой' }
    } finally { Pop-Location }
}

function Remove-Resources([string]$pkgDir) {
    Get-ChildItem -Path $pkgDir -Filter 'zz_vanta_rsrc_windows_*.syso' -ErrorAction SilentlyContinue |
        Remove-Item -Force
}

function Build-Exe([string]$pkg, [string]$name, [string]$desc) {
    $pkgDir = Join-Path $Root $pkg
    $out = Join-Path $Dist "$name.exe"
    Log "Windows: собираю $name.exe ($Arch)"
    try {
        New-Resources $pkgDir $name $desc
        # GOOS/GOARCH только на время go build (go-winres выше должен
        # собираться под хост, иначе при -Arch arm64 на amd64 не запустится).
        $env:GOOS = 'windows'; $env:GOARCH = $Arch
        & go build -trimpath -ldflags $LdFlags -o $out "./$pkg"
        if ($LASTEXITCODE -ne 0) { throw "go build $pkg завершился с ошибкой" }
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
        Remove-Resources $pkgDir
    }
    Ok $out
}

# ---------- сборка ----------
Log "$AppName $AppVersion (build $AppBuild) -> windows/$Arch"

if (-not $OnlyDebugger) {
    Build-Exe '.' $AppName "$AppName - управление VPS"
}
if ($Debugger -or $OnlyDebugger) {
    Build-Exe 'debugger' "$AppName Debugger" "$AppName Debugger"
}

Ok "Готово: $Dist"
