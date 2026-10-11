#!/usr/bin/env bash
# Installs the Reddit Brunei News Bot as rootless Podman Quadlet services.
#
#   curl -fsSL https://raw.githubusercontent.com/dsychin/RedditBruneiNewsBot/main/deploy/install-quadlet.sh | bash
#
# Optional environment: REF (git ref to install, default main), START=0 (install only).
set -euo pipefail

REF="${REF:-main}"
RAW="https://raw.githubusercontent.com/dsychin/RedditBruneiNewsBot/${REF}"
UNITS=(
  cf-clearance-scraper.container
  redditbruneinewsbot.container
  redditbruneinewsbot-data.volume
  redditbruneinewsbot.network
)
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/containers/systemd"
ENV_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/redditbruneinewsbot"
ENV_FILE="$ENV_DIR/.env"

die() { echo "error: $*" >&2; exit 1; }

[ "$(id -u)" -ne 0 ] || die "run as a regular user, not root (rootless Quadlet)"
for cmd in curl podman systemctl; do
  command -v "$cmd" >/dev/null || die "$cmd is required"
done
[ -n "${XDG_RUNTIME_DIR:-}" ] || die "XDG_RUNTIME_DIR is not set; log in with a full session (not plain su)"

mkdir -p "$UNIT_DIR" "$ENV_DIR"
chmod 700 "$ENV_DIR"

echo "Downloading units (${REF}) to $UNIT_DIR"
for u in "${UNITS[@]}"; do
  curl -fsSL "$RAW/deploy/quadlet/$u" -o "$UNIT_DIR/$u"
done

# Never overwrite existing credentials.
if [ ! -f "$ENV_FILE" ]; then
  curl -fsSL "$RAW/.env.example" -o "$ENV_FILE"
  chmod 600 "$ENV_FILE"

  set_var() { # set_var KEY VALUE (values are written unquoted, as the env file requires)
    local tmp
    tmp="$(mktemp)"
    awk -v k="$1" -v v="$2" 'BEGIN{FS=OFS="="} $1==k {print k "=" v; next} {print}' "$ENV_FILE" >"$tmp"
    cat "$tmp" >"$ENV_FILE"
    rm -f "$tmp"
  }

  if [ -r /dev/tty ]; then
    echo "Enter Reddit credentials (input hidden for secrets)."
    read -r -p "REDDIT_CLIENT_ID: " v </dev/tty && set_var REDDIT_CLIENT_ID "$v"
    read -r -s -p "REDDIT_CLIENT_SECRET: " v </dev/tty && echo && set_var REDDIT_CLIENT_SECRET "$v"
    read -r -s -p "REDDIT_REFRESH_TOKEN: " v </dev/tty && echo && set_var REDDIT_REFRESH_TOKEN "$v"
    read -r -p "SUBREDDITS [testingground4bots]: " v </dev/tty
    [ -z "$v" ] || set_var SUBREDDITS "$v"
    read -r -p "DRY_RUN (preview only, no posting) [true]: " v </dev/tty
    set_var DRY_RUN "${v:-true}"
  else
    echo "No terminal available: edit $ENV_FILE, then run: systemctl --user start redditbruneinewsbot.service"
    exit 0
  fi
else
  echo "Keeping existing $ENV_FILE"
fi

if grep -q '=replace-me' "$ENV_FILE"; then
  die "$ENV_FILE still has placeholder values; edit it and run: systemctl --user start redditbruneinewsbot.service"
fi

systemctl --user daemon-reload

if [ "${START:-1}" = "1" ]; then
  systemctl --user start redditbruneinewsbot.service
  systemctl --user enable --now podman-auto-update.timer
  echo "Started. Logs: journalctl --user -u redditbruneinewsbot -f"
fi

if ! loginctl show-user "$USER" -p Linger 2>/dev/null | grep -q 'Linger=yes'; then
  echo "Note: run 'sudo loginctl enable-linger $USER' so the services keep running after you log out."
fi
