#!/usr/bin/env bash
# mikan installer for Ubuntu 22.04+/Debian 12+.
#
#   sudo bash install.sh --image-tar mikan-image.tar.gz
#   sudo bash install.sh --image ghcr.io/OWNER/mikan:latest --registry-user OWNER --registry-token-file token.txt
#
# Installs Docker if needed, generates the secret admin link and password, writes
# /opt/mikan (compose, .env, data), starts the panel and the node, and installs the
# `mikan` command. The admin password is printed once and stored nowhere in clear text.
set -euo pipefail

MIKAN_DIR=/opt/mikan
IMAGE=""
IMAGE_TAR=""
DOMAIN=""
EMAIL=""
PANEL_PORT=""
PUBLIC_HOST=""
REGISTRY_USER=""
REGISTRY_TOKEN_FILE=""
ASSUME_YES=0
FIREWALL=1
TUNE=1

c_ok=$'\033[1;32m' c_warn=$'\033[1;33m' c_err=$'\033[1;31m' c_dim=$'\033[2m' c_b=$'\033[1m' c_0=$'\033[0m'
log() { printf '%s▸%s %s\n' "$c_ok" "$c_0" "$*"; }
warn() { printf '%s!%s %s\n' "$c_warn" "$c_0" "$*" >&2; }
die() {
  printf '%s✗%s %s\n' "$c_err" "$c_0" "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Установка mikan — панели VPN на ядре mihomo.

  --image-tar ФАЙЛ           образ из архива (docker save | gzip)
  --image ОБРАЗ              образ из реестра, например ghcr.io/owner/mikan:latest
  --registry-user ИМЯ        логин реестра (для приватного образа)
  --registry-token-file ФАЙЛ файл с токеном реестра (токен не попадёт в историю shell)
  --host IP|ИМЯ              публичный адрес сервера (по умолчанию определяется сам)
  --domain ДОМЕН             домен для сертификата и ссылок (необязательно)
  --email EMAIL              email для Let's Encrypt (необязательно)
  --port ПОРТ                порт панели (по умолчанию случайный 20000–60000)
  --no-firewall              не трогать ufw
  --no-tune                  не включать BBR и буферы UDP
  --yes                      не задавать вопросов
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --image) IMAGE="$2"; shift 2 ;;
    --image-tar) IMAGE_TAR="$2"; shift 2 ;;
    --registry-user) REGISTRY_USER="$2"; shift 2 ;;
    --registry-token-file) REGISTRY_TOKEN_FILE="$2"; shift 2 ;;
    --host) PUBLIC_HOST="$2"; shift 2 ;;
    --domain) DOMAIN="$2"; shift 2 ;;
    --email) EMAIL="$2"; shift 2 ;;
    --port) PANEL_PORT="$2"; shift 2 ;;
    --no-firewall) FIREWALL=0; shift ;;
    --no-tune) TUNE=0; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "неизвестный параметр: $1" ;;
  esac
done

confirm() {
  [ "$ASSUME_YES" = 1 ] && return 0
  local answer
  read -r -p "$1 [Y/n] " answer </dev/tty || return 1
  case "$answer" in "" | y | Y | д | Д) return 0 ;; *) return 1 ;; esac
}

# tr's stderr goes to /dev/null: it reports a broken pipe once head has enough bytes.
rand() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom 2>/dev/null | head -c "$1" || true; }
rand_login() {
  printf '%s%s' "$(LC_ALL=C tr -dc 'a-z' </dev/urandom 2>/dev/null | head -c 1 || true)" \
    "$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom 2>/dev/null | head -c 11 || true)"
}

port_busy() { # port proto
  if [ "$2" = udp ]; then ss -Hlnu "sport = :$1" | grep -q .; else ss -Hlnt "sport = :$1" | grep -q .; fi
}

port_owner() {
  ss -Hlnp${2:0:1} "sport = :$1" 2>/dev/null | grep -o 'users:(("[^"]*"' | head -1 | cut -d'"' -f2
}

# ---------- checks ----------
[ "$(id -u)" = 0 ] || die "Запустите от root: sudo bash install.sh"
[ -r /etc/os-release ] && . /etc/os-release
case "${ID:-}" in
  ubuntu | debian) ;;
  *) warn "Установщик проверен на Ubuntu и Debian, у вас ${PRETTY_NAME:-неизвестная ОС}." ;;
esac
case "$(uname -m)" in
  x86_64 | amd64 | aarch64 | arm64) ;;
  *) die "Архитектура $(uname -m) не поддерживается (нужна amd64 или arm64)." ;;
esac
[ -f "$MIKAN_DIR/.env" ] && die "mikan уже установлен в $MIKAN_DIR. Обновление: mikan update"
[ -n "$IMAGE" ] || [ -n "$IMAGE_TAR" ] || { usage; die "укажите --image-tar или --image"; }
[ -z "$IMAGE_TAR" ] || [ -f "$IMAGE_TAR" ] || die "нет файла $IMAGE_TAR"
export DEBIAN_FRONTEND=noninteractive
# A fresh VPS keeps apt busy for minutes (hoster provisioning, unattended-upgrades);
# get.docker.com fails on the dpkg lock instead of waiting.
dpkg_busy() {
  if command -v fuser >/dev/null; then
    fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock /var/cache/apt/archives/lock >/dev/null 2>&1
  else
    pgrep -x apt-get >/dev/null || pgrep -x apt >/dev/null || pgrep -x dpkg >/dev/null
  fi
}
if dpkg_busy; then
  log "Жду, пока система закончит установку пакетов…"
  for _ in $(seq 1 120); do dpkg_busy || break; sleep 5; done
  dpkg_busy && die "Пакетный менеджер занят больше 10 минут. Повторите установку позже."
fi
# Some VPS images ship with half-configured packages (cloud-init waiting on a conffile
# question); every apt call then fails. Keep the hoster's config files and finish the job.
if [ -n "$(dpkg --audit 2>/dev/null || true)" ]; then
  log "Довожу незавершённую настройку пакетов хостера (dpkg --configure -a)…"
  dpkg --configure -a --force-confdef --force-confold >/dev/null 2>&1 || warn "dpkg --configure -a завершился с ошибкой"
fi
command -v curl >/dev/null || { apt-get update -qq && apt-get install -y -qq curl; }

# ---------- docker ----------
if ! command -v docker >/dev/null; then
  log "Ставлю Docker (официальный скрипт get.docker.com)…"
  curl -fsSL https://get.docker.com | sh >/dev/null
fi
systemctl enable --now docker >/dev/null 2>&1 || true
docker compose version >/dev/null 2>&1 || die "Нет docker compose v2. Обновите Docker."

# ---------- address and ports ----------
if [ -z "$PUBLIC_HOST" ]; then
  PUBLIC_HOST=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || curl -4 -fsS --max-time 5 https://ifconfig.me 2>/dev/null || true)
  [ -n "$PUBLIC_HOST" ] || PUBLIC_HOST=$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')
fi
[ -n "$PUBLIC_HOST" ] || die "Не удалось определить IP сервера, укажите --host."
log "Адрес сервера: ${c_b}${PUBLIC_HOST}${c_0}"
confirm "Верно?" || die "Запустите снова с --host ВАШ_IP"

busy=""
for spec in 443/tcp 443/udp 8443/tcp 8443/udp; do
  if port_busy "${spec%/*}" "${spec#*/}"; then
    busy="$busy\n  ${spec}: занят ($(port_owner "${spec%/*}" "${spec#*/}"))"
  fi
done
if [ -n "$busy" ]; then
  printf '%s✗%s Порты для VPN заняты:%b\n' "$c_err" "$c_0" "$busy" >&2
  die "Освободите их (например, остановите старую панель: Hiddify, 3x-ui, nginx) и запустите снова."
fi
if port_busy 80 tcp; then
  warn "Порт 80 занят ($(port_owner 80 tcp)) — сертификат Let's Encrypt не выпустится, пока он не освободится."
fi
if [ -z "$PANEL_PORT" ]; then
  while :; do
    PANEL_PORT=$(shuf -i 20000-60000 -n 1)
    port_busy "$PANEL_PORT" tcp || break
  done
elif port_busy "$PANEL_PORT" tcp; then
  die "Порт $PANEL_PORT занят."
fi

# ---------- image ----------
if [ -n "$IMAGE_TAR" ]; then
  log "Загружаю образ из $IMAGE_TAR…"
  IMAGE=$(docker load -i "$IMAGE_TAR" | awk -F': ' '/Loaded image/ {print $2}' | tail -1)
  [ -n "$IMAGE" ] || die "В архиве нет образа."
else
  if [ -n "$REGISTRY_TOKEN_FILE" ]; then
    [ -n "$REGISTRY_USER" ] || die "Для --registry-token-file нужен --registry-user"
    docker login "${IMAGE%%/*}" -u "$REGISTRY_USER" --password-stdin <"$REGISTRY_TOKEN_FILE" >/dev/null
  fi
  log "Скачиваю образ $IMAGE…"
  docker pull -q "$IMAGE" >/dev/null
fi

# ---------- files ----------
ADMIN_PATH=$(rand 24)
SUB_PATH=$(rand 12)
ADMIN_USER=$(rand_login)
PASSWORD=$(rand 32)
umask 077
mkdir -p "$MIKAN_DIR/data/panel" "$MIKAN_DIR/data/node" "$MIKAN_DIR/backups"
cat >"$MIKAN_DIR/.env" <<EOF
MIKAN_IMAGE=$IMAGE
PANEL_PORT=$PANEL_PORT
MIKAN_UFW=$FIREWALL
EOF
cat >"$MIKAN_DIR/compose.yaml" <<'EOF'
name: mikan

x-hardening: &hardening
  image: ${MIKAN_IMAGE}
  network_mode: host
  restart: unless-stopped
  user: "65532:65532"
  cap_drop: [ALL]
  cap_add: [NET_BIND_SERVICE]
  security_opt: ["no-new-privileges:true"]
  read_only: true
  tmpfs: ["/tmp:rw,size=64m"]
  logging:
    driver: json-file
    options: {max-size: "10m", max-file: "3"}

services:
  node:
    <<: *hardening
    entrypoint: ["/usr/local/bin/mikan-node"]
    environment:
      MIKAN_DATA_DIR: /data/node
      MIKAN_NODE_SOCKET: /run/mikan/node.sock
    volumes: ["./data:/data", "run:/run/mikan"]

  panel:
    <<: *hardening
    command: ["serve"]
    depends_on: [node]
    environment:
      MIKAN_DATA_DIR: /data/panel
      MIKAN_NODE_SOCKET: /run/mikan/node.sock
      MIKAN_PANEL_LISTEN: 0.0.0.0:${PANEL_PORT}
    volumes: ["./data:/data", "run:/run/mikan"]
    healthcheck:
      test: ["CMD", "/usr/local/bin/mikan", "health"]
      interval: 30s
      timeout: 5s
      retries: 3

volumes:
  run: {}
EOF
chmod 600 "$MIKAN_DIR/.env" "$MIKAN_DIR/compose.yaml"
# Containers run as uid 65532; a bind mount does not inherit ownership from the image.
chown -R 65532:65532 "$MIKAN_DIR/data"
chmod 700 "$MIKAN_DIR/data"

cd "$MIKAN_DIR"
log "Создаю администратора и секретную ссылку…"
bootstrap=(admin bootstrap --public-host "$PUBLIC_HOST" --port "$PANEL_PORT" --admin-path "$ADMIN_PATH" --sub-path "$SUB_PATH" --username "$ADMIN_USER" --password-stdin)
[ -z "$DOMAIN" ] || bootstrap+=(--domain "$DOMAIN")
[ -z "$EMAIL" ] || bootstrap+=(--email "$EMAIL")
printf '%s\n' "$PASSWORD" | docker compose run --rm --no-deps -T panel "${bootstrap[@]}" >/dev/null

# ---------- system tuning ----------
if [ "$TUNE" = 1 ]; then
  cat >/etc/sysctl.d/99-mikan.conf <<'EOF'
# BBR for TCP protocols, bigger UDP buffers for Hysteria2/TUIC (QUIC)
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
EOF
  sysctl --system >/dev/null 2>&1 || warn "Не удалось применить sysctl — продолжаю без BBR."
fi
if [ "$FIREWALL" = 1 ] && command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  for rule in "$PANEL_PORT/tcp" 80/tcp 443/tcp 443/udp 8443/tcp 8443/udp; do ufw allow "$rule" >/dev/null; done
  log "Открыл порты в ufw."
fi

# ---------- start ----------
log "Запускаю…"
docker compose up -d >/dev/null
ok=0
for _ in $(seq 1 60); do
  code=$(curl -sk -o /dev/null -w '%{http_code}' "https://127.0.0.1:$PANEL_PORT/$ADMIN_PATH/" || true)
  if [ "$code" = 200 ]; then ok=1; break; fi
  sleep 1
done
[ "$ok" = 1 ] || { docker compose logs --tail 50; die "Панель не ответила за минуту. Логи выше."; }

# ---------- server command ----------
cat >/usr/local/bin/mikan <<'CLI'
#!/usr/bin/env bash
set -euo pipefail
MIKAN_DIR=/opt/mikan
cd "$MIKAN_DIR"
dc() { docker compose --project-directory "$MIKAN_DIR" "$@"; }
admin() { dc exec -T panel mikan admin "$@"; }
env_get() { grep -E "^$1=" .env | cut -d= -f2-; }

wait_healthy() {
  local port path code
  port=$(env_get PANEL_PORT)
  path=$(admin url | sed -E 's#https?://[^/]+/##')
  for _ in $(seq 1 60); do
    code=$(curl -sk -o /dev/null -w '%{http_code}' "https://127.0.0.1:$port/$path" || true)
    [ "$code" = 200 ] && return 0
    sleep 1
  done
  return 1
}

case "${1:-help}" in
  status)
    dc ps
    dc exec -T panel mikan health >/dev/null && echo "Панель отвечает."
    ;;
  logs) shift; dc logs -f --tail 200 "$@" ;;
  url) admin url ;;
  reset-password) admin reset-password ;;
  reset-path) admin reset-path ;;
  disable-2fa) admin disable-2fa ;;
  inbound)
    shift
    if [ "${1:-}" != add ]; then admin inbound "$@"; exit; fi
    # stdout is "port/network"; the message goes to stderr.
    rule=$(admin inbound "$@")
    if [ "$(env_get MIKAN_UFW)" != 0 ] && command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
      ufw allow "${rule/-/:}" >/dev/null && echo "Открыл $rule в ufw."
    fi
    ;;
  restart) dc restart ;;
  backup)
    ts=$(date +%Y%m%d-%H%M%S)
    admin backup /data/panel/backup.db >/dev/null
    umask 077
    tar -czf "backups/mikan-$ts.tar.gz" .env compose.yaml data/panel/backup.db data/panel/tls data/node 2>/dev/null
    rm -f data/panel/backup.db
    echo "Бэкап: $MIKAN_DIR/backups/mikan-$ts.tar.gz"
    ;;
  restore)
    [ -f "${2:-}" ] || { echo "Использование: mikan restore ФАЙЛ.tar.gz" >&2; exit 1; }
    read -r -p "Текущие данные будут заменены из $2. Продолжить? [y/N] " a
    [ "$a" = y ] || [ "$a" = Y ] || exit 1
    dc down
    tar -xzf "$2" -C "$MIKAN_DIR"
    mv -f data/panel/backup.db data/panel/mikan.db
    rm -f data/panel/mikan.db-wal data/panel/mikan.db-shm
    chown -R 65532:65532 data
    dc up -d
    echo "Восстановлено."
    ;;
  update)
    old=$(env_get MIKAN_IMAGE)
    if [ -n "${2:-}" ] && [ -f "$2" ]; then
      new=$(docker load -i "$2" | awk -F': ' '/Loaded image/ {print $2}' | tail -1)
    else
      new="${2:-$old}"
      docker pull -q "$new" >/dev/null
    fi
    [ -n "$new" ] || { echo "Нет образа для обновления" >&2; exit 1; }
    "$0" backup >/dev/null
    sed -i "s#^MIKAN_IMAGE=.*#MIKAN_IMAGE=$new#" .env
    dc up -d
    if wait_healthy; then
      echo "Обновлено: $new"
    else
      echo "Новая версия не поднялась — возвращаю $old" >&2
      sed -i "s#^MIKAN_IMAGE=.*#MIKAN_IMAGE=$old#" .env
      dc up -d
      exit 1
    fi
    ;;
  uninstall)
    read -r -p "Остановить mikan и удалить команду? Данные останутся в $MIKAN_DIR. [y/N] " a
    [ "$a" = y ] || [ "$a" = Y ] || exit 1
    dc down
    rm -f /usr/local/bin/mikan /etc/sysctl.d/99-mikan.conf
    echo "Готово. Данные и бэкапы: $MIKAN_DIR (удалить: rm -rf $MIKAN_DIR)"
    ;;
  *)
    cat <<'EOF'
mikan — управление панелью
  status          состояние контейнеров
  logs [panel|node]  логи (Ctrl+C — выход)
  url             ссылка на панель
  reset-password  новый пароль администратора
  reset-path      новая секретная ссылка
  disable-2fa     выключить 2FA (если потерян телефон)
  inbound list    подключения: имя, пресет, порт
  inbound add ПРЕСЕТ [--port ПОРТ]  добавить подключение (vless_reality_grpc, trojan_reality, anytls…)
  backup          бэкап в /opt/mikan/backups
  restore ФАЙЛ    восстановить из бэкапа
  update [ОБРАЗ|ФАЙЛ.tar.gz]  обновить с откатом при ошибке
  restart         перезапуск
  uninstall       остановить и удалить команду
EOF
    ;;
esac
CLI
chmod 755 /usr/local/bin/mikan

URL="https://$PUBLIC_HOST:$PANEL_PORT/$ADMIN_PATH/"
[ -z "$DOMAIN" ] || URL="https://$DOMAIN:$PANEL_PORT/$ADMIN_PATH/"
cat <<EOF

${c_ok}Готово!${c_0} mikan работает.

  Панель:  ${c_b}$URL${c_0}
  Логин:   ${c_b}$ADMIN_USER${c_0}
  Пароль:  ${c_b}$PASSWORD${c_0}   ${c_dim}← показывается один раз, сохраните в менеджер паролей${c_0}

  Сертификат Let's Encrypt выпустится в течение минуты. До этого браузер
  покажет предупреждение о сертификате — это ожидаемо.

  Команды на сервере: ${c_b}mikan${c_0} (status, logs, url, backup, update, reset-password…)
EOF
