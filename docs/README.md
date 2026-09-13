# docs/ — ассеты документации

## `demo.gif` — анимированный обзор для README

На него ссылаются корневые [`README.md`](../README.md) и [`README-RU.md`](../README-RU.md).
GIF генерируется из сценария [`demo.tape`](demo.tape) утилитой
[VHS](https://github.com/charmbracelet/vhs) от Charm. Сценарий детерминирован и
гоняет встроенный демо-бэкенд (`-demo`) — **реальный Docker-хост не нужен**.

### Сгенерировать / обновить через Docker (любая ОС, в т. ч. Windows)

В образе vhs нет Go, поэтому сначала соберите linux-бинарник в корне репозитория —
сценарий пересобирает его только при отсутствии `./d9c`:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o d9c .
docker run --rm -v "$PWD:/vhs" ghcr.io/charmbracelet/vhs:v0.9.0 docs/demo.tape
```

```powershell
# Windows (PowerShell)
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go build -o d9c .
docker run --rm -v "${PWD}:/vhs" ghcr.io/charmbracelet/vhs:v0.9.0 docs/demo.tape
```

### Сгенерировать нативно

```sh
vhs docs/demo.tape      # пересоберёт docs/demo.gif (соберёт ./d9c, если его нет)
```

VHS требует `ffmpeg` и `ttyd`:

```sh
go install github.com/charmbracelet/vhs@latest   # или: brew install vhs
#   macOS:        brew install ffmpeg ttyd
#   Linux (apt):  sudo apt install ffmpeg ttyd
```

На Windows `ttyd` ставится тяжело — используйте вариант через Docker.

После генерации закоммитьте `docs/demo.gif`.

## `demo.png` — статический скриншот

Кадр раздела Containers (запасной вариант, например для статей). Обновить — снять новый
кадр (`go run . -demo`) и перезаписать `docs/demo.png` тем же именем.

## `social-preview.png` — картинка для соцсетей

1280×640 — последний кадр ролика из сценария [`social.tape`](social.tape):

```sh
docker run --rm -v "$PWD:/vhs" ghcr.io/charmbracelet/vhs:v0.9.0 docs/social.tape
docker run --rm -v "$PWD:/vhs" --entrypoint sh ghcr.io/charmbracelet/vhs:v0.9.0 -c \
  'ffmpeg -y -sseof -0.3 -i docs/social.gif -update 1 -frames:v 1 docs/social-preview.png && rm docs/social.gif'
```

Загружается вручную: GitHub → Settings → General → Social preview.

> Образ `vhs:latest` на момент 2026-09 молча не пишет выходные файлы — используйте `v0.9.0`.
