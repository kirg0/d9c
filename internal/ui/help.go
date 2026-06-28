package ui

import (
	"fmt"
	"strings"

	"d9c/internal/i18n"
	"d9c/internal/keymap"
	"d9c/internal/ui/cmdline"
	"d9c/internal/ui/styles"
)

type helpRow struct{ key, desc string }

// buildHelpContent renders the context-aware help text for the current view:
// global keys, resource-specific keys, commands and any plugins in scope.
func (m Model) buildHelpContent() string {
	var b strings.Builder
	section := func(title string, rows []helpRow) {
		b.WriteString(styles.HelpTitle.Render(title))
		b.WriteString("\n")
		for _, r := range rows {
			b.WriteString("  ")
			b.WriteString(styles.HelpKey.Render(fmt.Sprintf("%-16s", r.key)))
			b.WriteString(" ")
			b.WriteString(styles.HelpDesc.Render(r.desc))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	k := m.keys
	section(i18n.T("Навигация", "Navigation"), []helpRow{
		{"↑/↓  j/k", i18n.T("Перемещение по списку", "Move through the list")},
		{"PgUp/PgDn", i18n.T("Страница вверх/вниз", "Page up/down")},
		{k.Display(keymap.Filter), i18n.T("Фильтр по строкам", "Filter rows")},
		{k.Display(keymap.Command), i18n.T("Командная строка", "Command line")},
		{k.Display(keymap.Copy), i18n.T("Меню копирования", "Copy menu")},
		{k.Display(keymap.Refresh), i18n.T("Обновить", "Refresh")},
		{k.Display(keymap.Pause), i18n.T("Пауза/возобновление автообновления", "Pause/resume auto-refresh")},
		{k.Display(keymap.Help), i18n.T("Эта справка", "This help")},
		{"esc", i18n.T("Назад / закрыть режим", "Back / close mode")},
		{"q  Ctrl+C", i18n.T("Выход", "Quit")},
	})

	section(i18n.T("Фильтр ( / )", "Filter ( / )"), []helpRow{
		{i18n.T("текст", "text"), i18n.T("Подстрока (без регистра); несколько слов — И", "Substring (case-insensitive); multiple words — AND")},
		{"re:<rx>", i18n.T("Регулярное выражение (без регистра)", "Regular expression (case-insensitive)")},
		{"status:<s>", i18n.T("По статусу/состоянию (running/exited…)", "By status/state (running/exited…)")},
		{"label:k[=v]", i18n.T("По метке контейнера (ключ или ключ=значение)", "By container label (key or key=value)")},
		{"network:<n>", i18n.T("По подключённой сети (алиас net:)", "By attached network (alias net:)")},
	})

	if rows := m.resourceKeyRows(); len(rows) > 0 {
		section(m.resource.String()+i18n.T(" — клавиши", " — keys"), rows)
	}

	if cmds := cmdline.CommandsFor(m.pluginScope(), m.composeHostOps); len(cmds) > 0 {
		rows := make([]helpRow, 0, len(cmds))
		for _, c := range cmds {
			rows = append(rows, helpRow{":" + c.Name, c.Hint})
		}
		section(i18n.T("Команды ( : )", "Commands ( : )"), rows)
	}

	section(i18n.T("Разделы ( : )", "Sections ( : )"), []helpRow{
		{":containers :c", i18n.T("Контейнеры", "Containers")},
		{":images :img", i18n.T("Образы", "Images")},
		{":networks :net", i18n.T("Сети", "Networks")},
		{":volumes :vol", i18n.T("Тома", "Volumes")},
		{":compose :co", i18n.T("Compose-проекты", "Compose projects")},
		{":hosts :h", i18n.T("Сохранённые хосты", "Saved hosts")},
		{":events", i18n.T("Живой журнал событий Docker", "Live Docker events feed")},
		{":system df", i18n.T("Дисковая статистика демона", "Daemon disk usage")},
		{":system prune", i18n.T("Полная очистка (с подтверждением)", "Full cleanup (with confirmation)")},
		{":theme [name]", i18n.T("Сменить тему; без имени — выбор из списка с превью", "Switch theme; no name — pick from a list with preview")},
		{":lang [ru|en]", i18n.T("Сменить язык; без аргумента — выбор из списка", "Switch language; no arg — pick from a list")},
		{":interval <dur>", i18n.T("Интервал автообновления (pause/resume)", "Auto-refresh interval (pause/resume)")},
		{":alert cpu|mem <%>", i18n.T("Порог CPU/MEM для подсветки строк; off — выключить", "CPU/MEM threshold to highlight rows; off — disable")},
	})

	if plugs := m.plugins.ForScope(m.pluginScope()); len(plugs) > 0 {
		rows := make([]helpRow, 0, len(plugs))
		for _, p := range plugs {
			key := ":" + p.Name
			if p.Key != "" {
				key += " (" + p.Key + ")"
			}
			desc := p.Description
			if desc == "" {
				desc = p.Command
			}
			rows = append(rows, helpRow{key, desc})
		}
		section(i18n.T("Плагины", "Plugins"), rows)
	} else {
		b.WriteString(styles.HelpMuted.Render(i18n.T("Плагины не настроены — см. README.md (d9c-plugins.yaml)", "No plugins configured — see README.md (d9c-plugins.yaml)")))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// resourceKeyRows returns the view-specific key bindings for the help screen,
// using the (possibly remapped) keys from the active keymap.
func (m Model) resourceKeyRows() []helpRow {
	k := m.keys
	switch m.resource {
	case ViewContainers:
		return []helpRow{
			{k.Display(keymap.Inspect), i18n.T("Подробности (inspect)", "Details (inspect)")},
			{k.Display(keymap.Logs), i18n.T("Логи", "Logs")},
			{k.Display(keymap.Exec), i18n.T("Shell в контейнере (встроенный терминал; exit/Ctrl-D — выход, Ctrl+\\ — отсоединить)", "Shell in the container (embedded terminal; exit/Ctrl-D — quit, Ctrl+\\ — detach)")},
			{"f", i18n.T("Обзор файловой системы (enter/l — войти, bksp/h — вверх, d — скачать)", "Browse the filesystem (enter/l — open, bksp/h — up, d — download)")},
			{":cp [<local> <ctr-dir>]", i18n.T("Загрузить файл/каталог в контейнер (без аргументов — модалка с выбором файла)", "Upload a file/dir into the container (no args — modal file picker)")},
			{k.Display(keymap.Stats), i18n.T("Метрики CPU/MEM (stats)", "CPU/MEM metrics (stats)")},
			{"⇧N ⇧S ⇧C ⇧M", i18n.T("Сортировка: имя/статус/CPU/MEM (повтор — реверс)", "Sort: name/status/CPU/MEM (repeat — reverse)")},
			{k.Display(keymap.ToggleAll), i18n.T("Все / только running", "All / running only")},
			{k.Display(keymap.Select), i18n.T("Отметить для массовой операции", "Select for a bulk operation")},
		}
	case ViewCompose:
		rows := []helpRow{
			{"enter", i18n.T("Открыть контейнеры проекта", "Open the project's containers")},
			{k.Display(keymap.Inspect), i18n.T("Подробности (inspect)", "Details (inspect)")},
			{k.Display(keymap.Logs), i18n.T("Логи проекта", "Project logs")},
		}
		// Editing the compose file needs host filesystem access (SSH only).
		if m.composeHostOps {
			rows = append(rows, helpRow{k.Display(keymap.Edit), i18n.T("Редактировать compose-файл", "Edit the compose file")})
		}
		return rows
	case ViewHosts:
		return []helpRow{
			{"enter", i18n.T("Подключиться к хосту", "Connect to the host")},
			{"a", i18n.T("Добавить хост (форма)", "Add a host (form)")},
			{"e", i18n.T("Редактировать выбранный хост (форма)", "Edit the selected host (form)")},
			{"d", i18n.T("Удалить выбранный хост (с подтверждением)", "Delete the selected host (with confirmation)")},
			{"—", i18n.T("STATUS + агрегат docker info (контейнеры/образы/версия) по каждому хосту", "STATUS + docker info summary (containers/images/version) per host")},
		}
	case ViewImages:
		return []helpRow{
			{k.Display(keymap.Inspect), i18n.T("Подробности (inspect)", "Details (inspect)")},
			{":build [dir] [tag]", i18n.T("Собрать образ (без dir — модалка; стриминг)", "Build an image (no dir — modal; streaming)")},
			{":tag <new-ref>", i18n.T("Назначить тег выбранному образу", "Tag the selected image")},
			{":push", i18n.T("Запушить образ в реестр (форма логина/пароля, стриминг)", "Push the image to a registry (login/password form, streaming)")},
			{":history", i18n.T("История слоёв образа", "Image layer history")},
			{k.Display(keymap.Select), i18n.T("Отметить для массового удаления", "Select for bulk removal")},
			{"r", i18n.T("Удалить выбранные образы (при выборе; с подтверждением)", "Remove selected images (when selecting; with confirmation)")},
			{":rm [-f]", i18n.T("Удалить выбранные образы (без выбора — под курсором)", "Remove selected images (no selection — the one under the cursor)")},
		}
	case ViewNetworks, ViewVolumes:
		return []helpRow{
			{k.Display(keymap.Inspect), i18n.T("Подробности (inspect)", "Details (inspect)")},
		}
	}
	return nil
}
