package ui

import (
	"fmt"
	"strings"

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
	section("Навигация", []helpRow{
		{"↑/↓  j/k", "Перемещение по списку"},
		{"PgUp/PgDn", "Страница вверх/вниз"},
		{k.Display(keymap.Filter), "Фильтр по строкам"},
		{k.Display(keymap.Command), "Командная строка"},
		{k.Display(keymap.Copy), "Меню копирования"},
		{k.Display(keymap.Refresh), "Обновить"},
		{k.Display(keymap.Pause), "Пауза/возобновление автообновления"},
		{k.Display(keymap.Help), "Эта справка"},
		{"esc", "Назад / закрыть режим"},
		{"q  Ctrl+C", "Выход"},
	})

	section("Фильтр ( / )", []helpRow{
		{"текст", "Подстрока (без регистра); несколько слов — И"},
		{"re:<rx>", "Регулярное выражение (без регистра)"},
		{"status:<s>", "По статусу/состоянию (running/exited…)"},
		{"label:k[=v]", "По метке контейнера (ключ или ключ=значение)"},
		{"network:<n>", "По подключённой сети (алиас net:)"},
	})

	if rows := m.resourceKeyRows(); len(rows) > 0 {
		section(m.resource.String()+" — клавиши", rows)
	}

	if cmds := cmdline.CommandsFor(m.pluginScope(), m.composeHostOps); len(cmds) > 0 {
		rows := make([]helpRow, 0, len(cmds))
		for _, c := range cmds {
			rows = append(rows, helpRow{":" + c.Name, c.Hint})
		}
		section("Команды ( : )", rows)
	}

	section("Разделы ( : )", []helpRow{
		{":containers :c", "Контейнеры"},
		{":images :img", "Образы"},
		{":networks :net", "Сети"},
		{":volumes :vol", "Тома"},
		{":compose :co", "Compose-проекты"},
		{":hosts :h", "Сохранённые хосты"},
		{":events", "Живой журнал событий Docker"},
		{":system df", "Дисковая статистика демона"},
		{":system prune", "Полная очистка (с подтверждением)"},
		{":theme <name>", "Сменить цветовую тему на лету"},
		{":interval <dur>", "Интервал автообновления (pause/resume)"},
		{":alert cpu|mem <%>", "Порог CPU/MEM для подсветки строк; off — выключить"},
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
		section("Плагины", rows)
	} else {
		b.WriteString(styles.HelpMuted.Render("Плагины не настроены — см. README.md (d9c-plugins.yaml)"))
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
			{k.Display(keymap.Inspect), "Подробности (inspect)"},
			{k.Display(keymap.Logs), "Логи"},
			{k.Display(keymap.Exec), "Shell в контейнере (встроенный терминал; exit/Ctrl-D — выход, Ctrl+\\ — отсоединить)"},
			{"f", "Обзор файловой системы (enter/l — войти, bksp/h — вверх, d — скачать)"},
			{":cp <local> <ctr-dir>", "Загрузить файл/каталог в контейнер (docker cp)"},
			{k.Display(keymap.Stats), "Метрики CPU/MEM (stats)"},
			{"⇧N ⇧S ⇧C ⇧M", "Сортировка: имя/статус/CPU/MEM (повтор — реверс)"},
			{k.Display(keymap.ToggleAll), "Все / только running"},
			{k.Display(keymap.Select), "Отметить для массовой операции"},
		}
	case ViewCompose:
		rows := []helpRow{
			{"enter", "Открыть контейнеры проекта"},
			{k.Display(keymap.Inspect), "Подробности (inspect)"},
			{k.Display(keymap.Logs), "Логи проекта"},
		}
		// Editing the compose file needs host filesystem access (SSH only).
		if m.composeHostOps {
			rows = append(rows, helpRow{k.Display(keymap.Edit), "Редактировать compose-файл"})
		}
		return rows
	case ViewHosts:
		return []helpRow{
			{"enter", "Подключиться к хосту"},
			{"a", "Добавить хост (форма)"},
			{"e", "Редактировать выбранный хост (форма)"},
			{"d", "Удалить выбранный хост (с подтверждением)"},
			{"—", "STATUS + агрегат docker info (контейнеры/образы/версия) по каждому хосту"},
		}
	case ViewImages:
		return []helpRow{
			{k.Display(keymap.Inspect), "Подробности (inspect)"},
			{":build <dir> [tag]", "Собрать образ (стриминг)"},
			{":tag <new-ref>", "Назначить тег выбранному образу"},
			{":push", "Запушить образ в реестр (форма логина/пароля, стриминг)"},
			{":history", "История слоёв образа"},
		}
	case ViewNetworks, ViewVolumes:
		return []helpRow{
			{k.Display(keymap.Inspect), "Подробности (inspect)"},
		}
	}
	return nil
}
