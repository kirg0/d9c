# d9c — дорожная карта развития

Живой план развития. **После реализации пункта и прохождения quality gate
(`gofmt`/`vet`/`golangci-lint`/`go test`/`-race`/`build`) ставь `[x]` и при нужде
добавляй короткую пометку.** В новых сессиях сверяйся с этим файлом, чтобы понимать,
что уже сделано и что брать дальше.

Условные обозначения: `[x]` — готово, `[ ]` — запланировано.

---

## Сделано (фундамент + Фаза 1)

- [x] Подключение по TCP и SSH-туннелю, живой `:connect`, сохранённые хосты (CRUD)
- [x] Разделы: Containers / Images / Networks / Volumes / Compose / Hosts
- [x] Контейнеры: start/stop/restart/kill/rm, inspect, фильтр, copy-меню
- [x] Логи: `--tail/--since/--until`, поиск `/`, сохранение в файл
- [x] Метрики: CPU/MEM/Net/Disk через Stats API (клавиша `s`)
- [x] Массовые операции: множественный выбор `Space` + bulk stop/restart/kill/rm
- [x] Интерактивный `exec`: клавиша `x` и `:exec`, один путь TCP+SSH. Открывается
      встроенным терминалом в окне приложения (vt10x-эмулятор, шапка/футер на месте);
      ввод форвардится в сессию, `exit`/Ctrl-D — выход, Ctrl+\ — отсоединить
- [x] Compose: discovery по меткам, жизненный цикл, `up/pull/down` со стримингом,
      `config`, `edit`, `create`, `backup`, `restore`, каталог бэкапов (`:backups`),
      inspect, логи проекта, drill-down в контейнеры
- [x] Автореконнект при разрыве TCP/SSH (backoff + баннер «reconnecting…»)
- [x] Плагины: декларативные команды/клавиши из YAML, `${VAR}`-подстановка,
      интерактивный/фоновый запуск (см. README.md)
- [x] Экран помощи `?` (контекстная справка по разделу)
- [x] Demo-режим (`-demo`, FakeBackend), тесты (table-driven + teatest), README
- [x] Надёжность стримов (ревизия 2026-06): `ContainerLogs`/`ComposeLogs`/`Events`
      возвращают stop-handle — закрытие вьюхи/refresh/смена хоста освобождают
      соединение и продьюсер-горутину (раньше каждый показ логов и каждый `r`
      в Events утекали навсегда); логи TTY-контейнеров больше не портятся
      (мультиплекс-заголовок снимается только при Tty=false); строки >64KB не
      обрывают поток молча (буфер 1MB + ошибка сканера строкой в поток);
      ошибка создания сети/тома показывается внутри модалки
- [x] Метрики на хостах с большим числом контейнеров не мигают: сбор через
      one-shot stats (мгновенный, CPU% по дельте с кэшированным замером
      прошлого тика), свежие семплы мержатся поверх старых (пропавший из
      батча контейнер не «гаснет»), одновременно не более одного батча
      (statsInFlight)
- [x] Баг-ревизия таблиц/рендера: идентификационные колонки (NAME у
      volumes/hosts/compose) больше не обрезаются — длинные имена (анонимные
      тома = 64 hex-символа) ломали rm/inspect/connect/drill-down; `truncate`
      стал rune-безопасным (кириллица не превращалась в мусор); устранены две
      потенциальные паники рендера: events-строка из <3 токенов и
      logs-подсветка уровня при ToUpper-разнобайтовых рунах

---

## Фаза 2 — управление ресурсами (создавать, а не только смотреть)

- [x] `docker events` — живой журнал событий демона (create/die/oom/health)
      отдельным разделом/консолью (переиспользовать стрим-инфраструктуру);
      команда `:events`, клавиша `r` — обновить поток, `q/esc` — выход
- [x] Образы: `build` / `tag` / `push` / история слоёв (build/push — со стримингом);
      команды `:build <dir> [tag]`, `:tag <new-ref>`, `:push`, `:history`
      (build/push идут в консоль прогресса, history — в detail-вьюер)
- [x] Push в приватный реестр с логином/паролем: модальная форма
      (registry/username/password, пароль маскируется) открывается по `:push`;
      пустой username = анонимный push; креды кодируются в `RegistryAuth` и
      запоминаются на сессию по реестру (без хранения на диске, форма пред-
      заполняется при повторном push)
- [x] Создание сетей и томов (модальные формы, как hostform): `:create` в
      разделах Networks/Volumes открывает модалку. Сеть — name/driver/subnet/
      gateway (driver по умолчанию bridge, subnet→IPAM-пул); том — name/driver
      (по умолчанию local). Backend.CreateNetwork/CreateVolume + FakeBackend
- [x] Мастер запуска контейнера (`run`): image + ports + env + volumes —
      модалка `:run` в Containers (в Images подставляет выбранный образ);
      порты в формате `docker run -p` через nat.ParsePortSpecs, env/volumes
      через запятую; Backend.RunContainer = create+start с дружелюбными
      ошибками (нет образа → подсказка :pull, занятое имя, кривой порт)
- [x] Exec-мастер по аналогии с `run`: модальная форма запуска одноразового
      интерактивного контейнера ИЗ ОБРАЗА (аналог `docker run --rm -it`) —
      `:exec` в Images (образ подставляется), поля image/volumes/command
      (пустая команда = shell). Сессия открывается во встроенном терминале;
      Backend.RunInteractive = create(Tty+AutoRemove) → attach → start,
      закрытие панели принудительно удаляет контейнер. Ошибка (нет образа
      и т.п.) показывается внутри формы. `:exec` в Containers по-прежнему
      exec в выбранный контейнер
- [x] System: `docker system df` + полный `prune` с подтверждением — глобальные
      команды `:system df` (отчёт TYPE/TOTAL/ACTIVE/SIZE/RECLAIMABLE в
      detail-вьюере) и `:system prune` (остановленные контейнеры + сети +
      висячие образы + build-кэш; тома не трогает — у них свой :prune).
      Перед prune — универсальный оверлей подтверждения (ModeConfirm, y/esc,
      переиспользуемый); итог «освобождено N» в футере, частичный результат
      показывается даже при ошибке одного из шагов
- [x] Health-статус контейнеров — колонка HEALTH (healthy/unhealthy/starting,
      с цветом; «-» без healthcheck) в таблице Containers; вердикт парсится
      из строки Status демона (`parseHealth`), есть в copy-меню. Фаза 2 готова

## Фаза 3 — UX и качество жизни

- [x] Индикатор статуса сервера в шапке: зелёная точка (●) при живом
      соединении, красная при недоступности, жёлтая во время автореконнекта.
      Периодический `Backend.Ping` на тике (не больше одного в полёте —
      pingInFlight); результат тегируется поколением бэкенда (pingSeq),
      пинг, переживший смену хоста, отбрасывается; connection-error из пинга
      запускает тот же автореконнект, что и упавший fetch (общий хелпер
      maybeStartReconnect). Попутно вылечен флак teatest под -race:
      waitFor("nginx:1.25") после :images матчился кадром Containers
      (колонка IMAGE) — теперь ждём уникальный заголовок REPOSITORY:TAG
- [x] Колонка STATUS в разделе Hosts: доступность КАЖДОГО сохранённого хоста
      (зелёное «● up» / красное «● down» / «…» пока не проверен) — видно,
      к кому можно подключиться, не подключаясь. `docker.ProbeHost` =
      одноразовое соединение + Ping с бюджетом 5s; батч пингует все хосты
      конкурентно, не чаще раза в 10s и не больше одного батча в полёте;
      в demo-режиме пробы заглушены (тесты не лезут в сеть)
- [x] Сортировка колонок в Containers (имя/статус/CPU/MEM): shift-клавиши
      `N/S/C/M` выбирают колонку, повторное нажатие реверсит порядок (CPU/MEM
      по умолчанию по убыванию — самые нагруженные сверху, имя/статус по
      возрастанию). Индикатор `↑/↓<COL>` в шапке. Чистая `table.SortContainers`
      (стабильная, не мутирует вход, tie-break по имени), спецификация хранится
      в table.Model и переживает refresh
- [x] Расширенные фильтры: regex, по статусу/метке/сети — мини-язык в строке `/`:
      bare-слова (подстрока, И), `re:<rx>` (regexp, case-insensitive), `status:`,
      `label:k[=v]`, `network:`/`net:`. Чистый `filter.Matcher` (Compile→Match,
      table-tested), `docker.Container` получил поля Labels/Networks; ошибка
      regexp подсвечивается прямо в строке фильтра, синтаксис описан в `?` и README
- [x] Конфиг-файл + темы: YAML `d9c-config.yaml` (флаг `-config`) с `theme:` (встроенные
      палитры tokyonight/dracula/nord/gruvbox/solarized/catppuccin) и точечными
      переопределениями `colors:` (hex `#rgb`/`#rrggbb` или ANSI 0-255). `styles.Palette` +
      `styles.Apply(p)` пересобирают ВСЕ стили из 10 базовых цветов; пакет `internal/theme`
      (`Resolve`/`Load`) валидирует тему/ключ/значение, отсутствие файла = дефолт. Применяется
      в `main` до `ui.Run`
- [x] Настраиваемые keybindings: секция `keys:` в `d9c-config.yaml` переназначает
      действия normal-режима (inspect/logs/edit/exec/filter/command/toggle-all/
      stats/select/copy/refresh/help). Пакет `internal/keymap` (Default/Resolve/
      Load, валидация: неизвестное действие, пустая/зарезервированная клавиша,
      конфликт «одна клавиша — два действия»); навигация, Enter и q/esc/Ctrl+C
      фиксированы. `handleNormal` резолвит клавишу в `keymap.Action` (built-in
      бьёт плагин по той же клавише), справка `?` показывает актуальные клавиши
- [x] Управляемый интервал автообновления + пауза (`p`): клавиша `p` ставит
      автообновление на паузу/снимает (heartbeat статуса при этом продолжает
      тикать, ручной `r` работает); команда `:interval <dur>` (напр. `5s`)
      меняет интервал на лету (с clamp в [1s, 1h]), `:interval pause|resume`
      и `:interval` без аргумента показывает текущее состояние; флаг `-interval`
      задаёт стартовый интервал. Индикатор в шапке (`↻3s` / `⏸ paused`) и хинт
      `p` в футере. Тик читает `m.refreshInterval` при перепланировании — второй
      тик-цикл не плодится
- [x] Просмотр файловой системы контейнера / `docker cp`: обзор ФС клавишей `f`
      (или `:files [path]`) в Containers — навигация enter/l (войти), ⌫/h/- (вверх),
      d (скачать выбранное в рабочий каталог). Листинг через `ls -1Ap` внутри
      контейнера (демультиплекс stdcopy; нет `ls` → понятная ошибка). Скачивание —
      `CopyFromContainer` + распаковка tar (с защитой от path-traversal); загрузка
      в контейнер — команда `:cp <local> <container-dir>` (`CopyToContainer`,
      tar по базовому имени). Новый режим `ModeFSBrowser`, компонент
      `internal/ui/fsbrowser`; FakeBackend отдаёт демо-ФС

## Фаза 4 — масштаб и экосистема

- [x] Мульти-хост дашборд (агрегат по сохранённым хостам): **влит в раздел
      Hosts** (отдельной секции нет). В Hosts добавлены колонки
      CONTAINERS/RUNNING/IMAGES/VERSION из `docker info` — STATUS и счётчики
      берутся из ОДНОГО per-host саммари (one-shot connect+Info, батч конкурентно,
      throttle 10s, гвард одного батча). STATUS цветной (● up/down/…), батч
      инжектится (`summarizeHost`), в demo/тестах заглушён (без сети). `:dashboard`
      /`:dash` — алиасы для `:hosts`. Backend.Info + docker.ProbeHostSummary
      (старый lightweight docker.ProbeHost удалён — саммари его заменил).
      Управление хостами в разделе — клавишами `a` (добавить) / `e` (редактировать)
      / `d` (удалить с подтверждением через ModeConfirm), помимо `:add`/`:edit`/`:rm`
- [x] Алерты по порогам ресурсов (CPU/MEM; опора на Stats API): пакет
      `internal/alerts` (Thresholds CPU/MEM, Evaluate→[]Breach, Load из секции
      `alerts:` в d9c-config.yaml). Контейнер с CPU%/MEM% ≥ порога подсвечивается
      маркером `⚠` у имени (обе раскладки таблицы Containers; цвет — nameAlertStyle/
      styles.Alert), в шапке — счётчик `⚠ N`. Команда `:alert cpu|mem <%>` /
      `cpu|mem off` / `off` / без аргумента (показать пороги). Тип Colorizer получил
      override-флаг (passthrough к base для не-горящих строк). Disk опущен: у
      Stats API нет per-container «диск %» (Block I/O кумулятивен)

## Инфраструктура (можно параллельно)

- [x] Контроль версий через git + версионирование приложения по SemVer
      (первая версия `1.0.0`). Далее: новая фича → минор, фикс бага → патч.
      Перед работой отводим ветку от `main`, коммитим в неё, после завершения
      вливаем в `main`. Реализовано: репозиторий (`.gitignore`/`.gitattributes`),
      пакет `internal/version` (overridable `-ldflags`), флаг `-version`
      (печатает `d9c v1.0.0` и выходит), версия в шапке. Тег `v1.0.0`
- [ ] CI (GitHub Actions: `make check` + `go test -race`)
- [ ] Релизные бинарники (goreleaser) под Linux/macOS/Windows
- [ ] Проверка/доводка абстракции `docker.Backend` под Podman

## Техдолг / надёжность

- [x] Stop-handle для конечных прогресс-стримов (`BuildImage`/`PushImage`/
      `ComposeUp`/`ComposePull`/`ComposeDown`/`CreateComposeFile`/
      `RestoreComposeProject`, плюс плагинный `streamLocalProcess`): теперь
      возвращают `(ch, stop, err)` по тому же паттерну done+stop, что logs/events.
      Прогресс-консоль переиспользует `ModeLogs`, поэтому `opStartedMsg` несёт
      `stop` и кладёт его в `m.logStop` — закрытие консоли (`q`/глобальный esc),
      refresh и смена хоста рвут стрим (раньше продьюсер висел на send после
      заполнения буфера 256 вместе с SSH-сессией/запросом к демону до конца
      процесса). `streamDockerJSON`/`sshExecStream`/`streamLocalProcess` закрывают
      нижележащий ресурс (reader/SSH session/процесс) и зовут `stop` на
      естественном конце; `RestoreComposeProject` пробрасывает stop внутреннего
      `up`-стрима. Тесты: `TestStreamDockerJSONStopUnblocksProducer` (docker),
      `TestProgressConsoleStopOnClose`/`OnEsc` (ui)
- [x] `update.go` разросся (~2090 строк) — dispatch-таблицы команд и хендлеры
      форм вынесены в отдельные файлы. `update_dispatch.go` (13 функций):
      `dispatchCommand` + per-view `dispatchHost/Compose/ImageCommand` и их
      хелперы (`setAlertThreshold`/`alertSummary`/`parseLogOptions`/
      `targetContainerIDs`/`bulkAction`/`selectedImageRef`/`imageRefFromTags`/
      `firstNonEmpty`/`saveHostsThenRefresh`). `update_forms.go` (7 функций):
      хендлеры модальных форм `handleHost/Push/Net/Vol/Run/ExecForm` + `splitList`.
      В `update.go` остался Elm-цикл `Update`, роутинг клавиш (`handleKey`/
      `handleNormal`/`handleAction`) и overlay-хендлеры. Чистый перенос целых
      функций (поведение не менялось), пакет один — `update_test.go` без правок;
      gate зелёный (gofmt/vet/golangci-lint 0 issues/test/-race)

---

## Исключено по решению пользователя

- История действий (action history) — убрана из ТЗ.
