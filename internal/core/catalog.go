package core

import "strings"

// Route — адрес раздела: "overview", "system/services", "settings/app".
type Route string

const RouteOverview Route = "overview"

// Page — пункт навигации.
type Page struct {
	Route       Route
	Title       string
	Description string
}

// Section — пункт второго уровня header. Overview в навигации нет: туда ведёт логотип.
type Section struct {
	ID    string
	Title string
	Pages []Page
}

func p(route, title, desc string) Page {
	return Page{Route: Route(route), Title: title, Description: desc}
}

// Sections — навигация по спецификации header v1.1.
var Sections = []Section{
	{ID: "monitor", Title: "Monitor", Pages: []Page{
		p("monitor/resources", "Resources", "CPU, память и нагрузка"),
		p("monitor/processes", "Processes", "Запущенные процессы"),
		p("monitor/network", "Network", "Интерфейсы, порты, трафик"),
		p("monitor/storage", "Storage", "Диски и разделы"),
	}},
	{ID: "system", Title: "System", Pages: []Page{
		p("system/services", "Services", "Управление systemd-сервисами"),
		p("system/journal", "Journal", "Системные логи и события"),
		p("system/users", "Users", "Учётные записи и права"),
		p("system/packages", "Packages", "Установленное ПО"),
		p("system/cron", "Cron", "Задачи по расписанию"),
	}},
	{ID: "developer", Title: "Developer", Pages: []Page{
		p("developer/terminal", "Terminal", "Командная строка сервера"),
		p("developer/files", "Files", "Файловый менеджер"),
		p("developer/docker", "Docker", "Контейнеры, образы, тома"),
		p("developer/databases", "Databases", "PostgreSQL, Redis и другие"),
	}},
	{ID: "automation", Title: "Automation", Pages: []Page{
		p("automation/functions", "Functions", "Свои функции и скрипты"),
		p("automation/macros", "Macros", "Цепочки команд"),
		p("automation/workflows", "Workflows", "Сценарии с условиями"),
		p("automation/scheduler", "Scheduler", "Запуск по расписанию"),
	}},
	{ID: "security", Title: "Security", Pages: []Page{
		p("security/firewall", "Firewall", "Правила ufw / nftables"),
		p("security/ssh", "SSH", "Ключи и настройки sshd"),
		p("security/audit", "Audit", "Журнал действий"),
		p("security/access", "Access", "Доступ к серверам"),
	}},
	{ID: "more", Title: "More", Pages: []Page{
		p("more/backups", "Backups", "Бэкапы и восстановление"),
	}},
}

// SettingsPages — пункты меню Settings в header.
var SettingsPages = []Page{
	p("settings/app", "Настройки приложения", "Общие параметры VANTA"),
	p("settings/appearance", "Внешний вид", "Тема и отображение"),
	p("settings/integrations", "Интеграции", "Telegram, S3, вебхуки"),
}

// PageOf — заголовок раздела и путь «Секция › Страница» для route.
func PageOf(r Route) (title, trail string) {
	if r == RouteOverview || r == "" {
		return "Overview", "Overview"
	}
	for _, s := range Sections {
		for _, pg := range s.Pages {
			if pg.Route == r {
				return pg.Title, s.Title + " › " + pg.Title
			}
		}
	}
	for _, pg := range SettingsPages {
		if pg.Route == r {
			return pg.Title, "Settings › " + pg.Title
		}
	}
	return string(r), string(r)
}

// SectionOf — ID пункта навигации, к которому относится route
// (для подсветки активного пункта). Пусто — ни к какому.
func SectionOf(r Route) string {
	if r == RouteOverview {
		return "overview"
	}
	id, _, _ := strings.Cut(string(r), "/")
	for _, s := range Sections {
		if s.ID == id {
			return id
		}
	}
	return ""
}
