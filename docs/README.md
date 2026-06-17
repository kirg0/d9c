# docs/ — ассеты документации

## `demo.png` — скриншот для README

Статический скриншот раздела Containers; на него ссылается корневой
[`README.md`](../README.md). Чтобы обновить — снимите новый кадр (`go run . -demo`
или реальный хост) и перезапишите `docs/demo.png` тем же именем.

## `demo.gif` — анимированный обзор (опционально)

GIF генерируется из сценария [`demo.tape`](demo.tape) утилитой
[VHS](https://github.com/charmbracelet/vhs) от Charm. Сценарий детерминирован и
гоняет встроенный демо-бэкенд (`-demo`) — **реальный Docker не нужен**.

### Сгенерировать / обновить

Из корня репозитория:

```sh
vhs docs/demo.tape      # пересоберёт docs/demo.gif
```

### Установка VHS

VHS требует `ffmpeg` и `ttyd`.

```sh
# через Go
go install github.com/charmbracelet/vhs@latest

# macOS (Homebrew) — тянет зависимости автоматически
brew install vhs

# затем установите ffmpeg и ttyd, если их нет:
#   macOS:        brew install ffmpeg ttyd
#   Linux (apt):  sudo apt install ffmpeg ttyd
#   Windows:      проще всего собрать GIF из WSL/Linux или macOS
```

> На Windows `ttyd` ставится тяжело — удобнее сгенерировать GIF из WSL, Linux или
> macOS, закоммитить готовый `docs/demo.gif` и отредактировать `demo.tape` при
> необходимости.

После генерации закоммитьте `docs/demo.gif`. Чтобы показывать в README анимацию
вместо скриншота, поменяйте `src="docs/demo.png"` на `src="docs/demo.gif"` в
корневом [`README.md`](../README.md).
