#!/usr/bin/env bash
# =====================================================================
#  VANTA — сборка для macOS / Linux
#
#  ./build.sh                     сборка под текущую ОС и архитектуру
#  ./build.sh --arch universal    macOS: универсальный .app (arm64 + amd64)
#  ./build.sh --arch amd64        конкретная архитектура (arm64 | amd64)
#  ./build.sh --debugger          дополнительно собрать debugger/
#  ./build.sh --only-debugger     собрать только debugger/
#  ./build.sh --dmg               macOS: дополнительно упаковать в .dmg
#  ./build.sh --clean             удалить dist/ перед сборкой
#
#  Результат: dist/<os>-<arch>/
#    macOS — Vanta.app (запускается без терминала, с иконкой в доке)
#    Linux — бинарник vanta (+ vanta.desktop и иконка)
#
#  Windows: используйте build.ps1 / build.bat.
# =====================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# ---------- метаданные из FyneApp.toml ----------
toml_get() { # toml_get Key -> значение (первое совпадение)
    sed -nE "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*\"?([^\"]*)\"?[[:space:]]*$/\1/p" FyneApp.toml | head -n1
}
APP_NAME="$(toml_get Name)";     APP_NAME="${APP_NAME:-Vanta}"
APP_ID="$(toml_get ID)";         APP_ID="${APP_ID:-org.trade-cloud.vanta}"
APP_VERSION="$(toml_get Version)"; APP_VERSION="${APP_VERSION:-0.1.0}"
APP_BUILD="$(toml_get Build)";   APP_BUILD="${APP_BUILD:-1}"
ICON_DIR="assets/app-icon"
ICON_PNG="$ICON_DIR/mac-1024.png"

DBG_NAME="$APP_NAME Debugger"
DBG_ID="$APP_ID.debugger"

# ---------- аргументы ----------
ARCH=""
WITH_MAIN=1
WITH_DEBUGGER=0
MAKE_DMG=0
CLEAN=0

usage() { sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --arch)          ARCH="${2:-}"; shift 2 ;;
        --arch=*)        ARCH="${1#*=}"; shift ;;
        --debugger)      WITH_DEBUGGER=1; shift ;;
        --only-debugger) WITH_DEBUGGER=1; WITH_MAIN=0; shift ;;
        --dmg)           MAKE_DMG=1; shift ;;
        --clean)         CLEAN=1; shift ;;
        -h|--help)       usage ;;
        *) echo "Неизвестный аргумент: $1 (см. --help)" >&2; exit 1 ;;
    esac
done

# ---------- окружение ----------
command -v go >/dev/null 2>&1 || { echo "✗ Go не найден в PATH" >&2; exit 1; }

case "$(uname -s)" in
    Darwin) OS=darwin ;;
    Linux)  OS=linux ;;
    *) echo "✗ Неподдерживаемая ОС: $(uname -s). Для Windows — build.ps1" >&2; exit 1 ;;
esac

HOST_ARCH="$(go env GOARCH)"
ARCH="${ARCH:-$HOST_ARCH}"
case "$ARCH" in
    arm64|amd64) ;;
    universal) [[ $OS == darwin ]] || { echo "✗ universal только для macOS" >&2; exit 1; } ;;
    *) echo "✗ --arch: arm64 | amd64 | universal" >&2; exit 1 ;;
esac

# Fyne (и internal/dockicon) требуют cgo.
export CGO_ENABLED=1
LDFLAGS="-s -w"
DIST="$ROOT/dist/$OS-$ARCH"

log()  { printf '\033[1;38;5;208m▸\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m✓\033[0m %s\n' "$*"; }

if [[ $CLEAN == 1 ]]; then log "Удаляю dist/"; rm -rf "$ROOT/dist"; fi
mkdir -p "$DIST"

# go build для одной архитектуры: gobuild <pkg> <out> <arch>
gobuild() {
    local pkg="$1" out="$2" arch="$3"
    local cc=()
    if [[ $OS == darwin ]]; then
        # Кросс-сборка arm64 <-> amd64 на маке через clang -arch
        local carch="$arch"; [[ $arch == amd64 ]] && carch=x86_64
        cc=(CC="clang -arch $carch" CXX="clang++ -arch $carch")
        export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}"
    fi
    env GOOS="$OS" GOARCH="$arch" ${cc[@]+"${cc[@]}"} \
        go build -trimpath -ldflags "$LDFLAGS" -o "$out" "$pkg"
}

# бинарник под выбранную архитектуру (universal -> lipo): build_bin <pkg> <out>
build_bin() {
    local pkg="$1" out="$2"
    if [[ $ARCH == universal ]]; then
        local tmp; tmp="$(mktemp -d)"
        gobuild "$pkg" "$tmp/arm64" arm64
        gobuild "$pkg" "$tmp/amd64" amd64
        lipo -create -output "$out" "$tmp/arm64" "$tmp/amd64"
        rm -rf "$tmp"
    else
        gobuild "$pkg" "$out" "$ARCH"
    fi
}

# ---------- macOS: .icns из assets/app-icon ----------
make_icns() { # make_icns <out.icns>
    local out="$1" set; set="$(mktemp -d)/AppIcon.iconset"
    mkdir -p "$set"
    local pairs=(
        "16:icon_16x16"   "32:icon_16x16@2x"
        "32:icon_32x32"   "64:icon_32x32@2x"
        "128:icon_128x128" "256:icon_128x128@2x"
        "256:icon_256x256" "512:icon_256x256@2x"
        "512:icon_512x512" "1024:icon_512x512@2x"
    )
    local p size name
    for p in "${pairs[@]}"; do
        size="${p%%:*}"; name="${p#*:}"
        if [[ -f "$ICON_DIR/mac-$size.png" ]]; then
            cp "$ICON_DIR/mac-$size.png" "$set/$name.png"
        else
            sips -z "$size" "$size" "$ICON_PNG" --out "$set/$name.png" >/dev/null
        fi
    done
    iconutil -c icns "$set" -o "$out"
    rm -rf "$(dirname "$set")"
}

# ---------- macOS: .app-бандл ----------
# make_app <pkg> <Имя> <bundle id> <имя бинарника>
make_app() {
    local pkg="$1" name="$2" id="$3" exe="$4"
    local app="$DIST/$name.app"
    log "macOS: собираю $name.app ($ARCH)"
    rm -rf "$app"
    mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

    build_bin "$pkg" "$app/Contents/MacOS/$exe"
    make_icns "$app/Contents/Resources/icon.icns"

    cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>                   <string>$name</string>
    <key>CFBundleDisplayName</key>            <string>$name</string>
    <key>CFBundleExecutable</key>             <string>$exe</string>
    <key>CFBundleIdentifier</key>             <string>$id</string>
    <key>CFBundleIconFile</key>               <string>icon.icns</string>
    <key>CFBundlePackageType</key>            <string>APPL</string>
    <key>CFBundleInfoDictionaryVersion</key>  <string>6.0</string>
    <key>CFBundleShortVersionString</key>     <string>$APP_VERSION</string>
    <key>CFBundleVersion</key>                <string>$APP_BUILD</string>
    <key>CFBundleDevelopmentRegion</key>      <string>ru</string>
    <key>LSMinimumSystemVersion</key>         <string>${MACOSX_DEPLOYMENT_TARGET:-11.0}</string>
    <key>LSApplicationCategoryType</key>      <string>public.app-category.developer-tools</string>
    <key>NSHighResolutionCapable</key>        <true/>
    <key>NSSupportsAutomaticGraphicsSwitching</key> <true/>
    <key>NSRequiresAquaSystemAppearance</key> <false/>
</dict>
</plist>
PLIST
    printf 'APPL????' > "$app/Contents/PkgInfo"
    plutil -lint "$app/Contents/Info.plist" >/dev/null

    # Ad-hoc подпись: без неё Apple Silicon может не запустить бинарник
    # после правок бандла. Для раздачи нужна подпись Developer ID + нотаризация.
    codesign --force --deep --sign - "$app" >/dev/null 2>&1 || true
    xattr -cr "$app" 2>/dev/null || true

    ok "$app"
}

make_dmg() { # make_dmg <Имя>
    local name="$1" dmg="$DIST/$1-$APP_VERSION-$ARCH.dmg" stage
    log "macOS: упаковываю $(basename "$dmg")"
    stage="$(mktemp -d)"
    cp -R "$DIST/$name.app" "$stage/"
    ln -s /Applications "$stage/Applications"
    rm -f "$dmg"
    hdiutil create -volname "$name" -srcfolder "$stage" -ov -format UDZO "$dmg" >/dev/null
    rm -rf "$stage"
    ok "$dmg"
}

# ---------- Linux: бинарник + .desktop ----------
make_linux() { # make_linux <pkg> <Имя> <имя бинарника>
    local pkg="$1" name="$2" exe="$3"
    log "Linux: собираю $exe ($ARCH)"
    build_bin "$pkg" "$DIST/$exe"
    cp "$ICON_PNG" "$DIST/$exe.png"
    cat > "$DIST/$exe.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=$name
Exec=$exe
Icon=$exe
Terminal=false
Categories=Development;System;Network;
StartupWMClass=$name
DESKTOP
    ok "$DIST/$exe"
}

# ---------- сборка ----------
log "$APP_NAME $APP_VERSION (build $APP_BUILD) → $OS/$ARCH"

if [[ $WITH_MAIN == 1 ]]; then
    if [[ $OS == darwin ]]; then
        make_app "." "$APP_NAME" "$APP_ID" "vanta"
        if [[ $MAKE_DMG == 1 ]]; then make_dmg "$APP_NAME"; fi
    else
        make_linux "." "$APP_NAME" "vanta"
    fi
fi

if [[ $WITH_DEBUGGER == 1 ]]; then
    if [[ $OS == darwin ]]; then
        make_app "./debugger" "$DBG_NAME" "$DBG_ID" "vanta-debugger"
    else
        make_linux "./debugger" "$DBG_NAME" "vanta-debugger"
    fi
fi

ok "Готово: $DIST"
