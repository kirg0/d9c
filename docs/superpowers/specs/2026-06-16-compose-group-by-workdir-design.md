# Compose: группировка деплоев по working_dir

Дата: 2026-06-16. Статус: согласовано. Версия выпуска: v1.0.10 (исправление поведения → патч).

## Проблема

Раздел Compose группирует контейнеры только по метке `com.docker.compose.project`.
На хостах, где несколько независимых compose-файлов (каждый в своём каталоге) делят одно
имя проекта, они схлопываются в одну запись и «не отражают действительность».

Реальный пример (хост mp10-280): сервисы `core.licensing` и `platform_triggers` оба имеют
`com.docker.compose.project=mcmc`, но разные `working_dir`
(`/var/lib/deployed-roles/MC/MC/images/core.licensing` и `.../platform_triggers`) и разные
`config_files` (по своему `docker-compose.yaml`). Сейчас они валятся в один проект `mcmc`.

Дополнительная опасность: lifecycle-операции фильтруют контейнеры по метке `project`, поэтому
операция над одним «деплоем» затронула бы **все** контейнеры `mcmc`.

## Решение

### 1. Группировка (`internal/docker/compose.go`, `ListComposeProjects`)
Ключ группировки — `working_dir` (метка `com.docker.compose.project.working_dir`).
Каждый уникальный working_dir = отдельная запись (деплой). Когда метка working_dir
отсутствует (старый compose), фолбэк на имя project — нормальные одно-каталожные проекты
ведут себя как сегодня.

### 2. Структура `ComposeProject`
- Добавить поле `Project string` — docker-имя проекта (`com.docker.compose.project`).
- `Name` — имя деплоя для показа = `basename(working_dir)`; фолбэк — `Project`, если working_dir пуст.
- `WorkingDir` (уже есть) — identity деплоя.
- `ConfigFiles`, `Status`, `Command`, `Running`, `Total` — без изменений.

### 3. Identity и scoping операций
Новый хелпер `composeFilter(identity string) filters.Args`:
- если identity выглядит как путь (содержит `/` или `\`) → фильтр по метке `working_dir=identity`;
- иначе → фильтр по метке `project=identity`.

Обоснование различения: имена docker-проектов ограничены `[a-z0-9_-]` и не содержат разделителей
пути; working_dir всегда абсолютный путь. Различение надёжно.

Все Compose-операции принимают `identity` (working_dir деплоя) и используют `composeFilter`:
`ListComposeContainers`, `InspectComposeProject`, `ComposeLogs`, `ComposeStart/Stop/Restart/
Pause/Unpause/Remove`, `ComposeUp/Down/Pull/Config`, `ReadComposeFile/WriteComposeFile`,
`BackupComposeProject`, `RestoreComposeProject`. (`CreateComposeFile` работает по каталогу —
не меняется.)

SSH-команды `docker compose` по-прежнему собираются как
`-p <project> --project-directory <working_dir> -f <config_files>`, где `project` и
`config_files` читаются из меток контейнеров, найденных по identity. Так операции корректно
ограничены одним деплоем.

### 4. Таблица (`internal/ui/table/table.go`)
Новый порядок колонок: **PROJECT | NAME | PATH | STATUS | COMMAND**.
- PROJECT = `p.Project` (напр. `mcmc`).
- NAME = `p.Name` (имя деплоя, basename каталога — напр. `core.licensing`).
- PATH = полный `p.WorkingDir` — identity-колонка, **не обрезается** (таблица bubbles клипует
  визуально по ширине, как для NAME контейнеров / ID).
- STATUS, COMMAND — как сейчас.

Фильтр-таргет (поиск) расширить полем PROJECT.

### 5. `selectedID()` (`internal/ui/model.go`)
Для `ViewCompose` возвращать working_dir (значение PATH-колонки) вместо `row[0]`:
PROJECT теперь первый и не уникален. Индекс identity-колонки вынести в именованную константу,
экспортируемую из пакета table (напр. `ComposeIDColumn`), чтобы `selectedID` не зависел от
магического числа.

### 6. Drill-down / хлебные крошки
`Enter` по деплою: `composeFilter = identity (working_dir)`; `fetchComposeContainers` →
`ListComposeContainers(working_dir)`. В хлебных крошках показывать **имя деплоя** (basename),
а не длинный путь.

### 7. FakeBackend (`internal/docker/fake.go`)
- Проставить `Project` у самплов (= текущему Name).
- Операции (`ListComposeContainers` и пр.) матчат по identity = working_dir.
- Demo-самплы (webapp `/srv/webapp`, monitoring `/srv/monitoring`, legacy `/opt/legacy`) имеют
  уникальные каталоги; basename совпадает с прежним Name → 3 записи, существующие teatests живут
  (с поправкой на то, что identity теперь working_dir).

## Тесты
- `compose_test.go`: новая группировка — два контейнера один project + разные working_dir → 2
  группы; один working_dir → 1 группа; фолбэк без working_dir → группировка по project.
- `composeFilter`: путь → working_dir-метка; имя → project-метка (table-driven).
- table: новые колонки и identity-колонка (`buildComposeRows`, `ComposeIDColumn`).
- `selectedID` для compose возвращает working_dir.
- Правка существующих compose-teatest и unit-тестов под новую identity.
- Сквозная проверка на реальном хосте mp10-280 — **только чтение** (list/inspect), без мутаций.

## Вне scope (YAGNI)
- Не меняем `CreateComposeFile` (он по каталогу).
- Не добавляем настройку «группировать по project|workdir» — поведение единое и корректное.
- Никакого несвязанного рефакторинга compose.go.
