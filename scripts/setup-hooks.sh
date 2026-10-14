#!/usr/bin/env bash
#
# setup-hooks.sh — устанавливает git hooks проекта.
# Запускать один раз после клонирования.
#
set -euo pipefail

HOOKS_DIR=".git/hooks"

if [ ! -d "$HOOKS_DIR" ]; then
    echo "ошибка: .git/hooks не найден. Инициализируйте git." >&2
    exit 1
fi

cat > "$HOOKS_DIR/prepare-commit-msg" <<'HOOK_EOF'
#!/usr/bin/env bash
set -e
MSG_FILE="$1"
SOURCE="$2"

case "$SOURCE" in
    merge|squash|commit) exit 0 ;;
esac

if ! grep -q '^Assisted-by:' "$MSG_FILE"; then
    if [ -s "$MSG_FILE" ] && [ -n "$(tail -c 1 "$MSG_FILE")" ]; then
        echo "" >> "$MSG_FILE"
    fi
    echo "Assisted-by: DeepSeek chat" >> "$MSG_FILE"
fi
HOOK_EOF

chmod +x "$HOOKS_DIR/prepare-commit-msg"
echo "✓ установлен $HOOKS_DIR/prepare-commit-msg"
