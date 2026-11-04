package host

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DB-движки.
const (
	DBPostgres = "postgres"
	DBMySQL    = "mysql" // и MariaDB
	DBRedis    = "redis"
	DBMongo    = "mongo"
)

// DBEngine — найденная СУБД: на самом сервере или в docker-контейнере.
type DBEngine struct {
	Kind       string
	Label      string // «PostgreSQL 16 (сервер)» / «redis в контейнере cache»
	Container  string // ID контейнера, "" — на сервере
	DockerRoot bool   // docker нужен через sudo
	Version    string
}

// DBInfo — база данных в СУБД.
type DBInfo struct {
	Name string
	Size uint64 // байты, 0 — неизвестно
	Note string // redis: keys=…
}

// DetectDatabases ищет СУБД на сервере (бинарь клиента + запущенный
// процесс) и в запущенных контейнерах (по образу).
func (h *Host) DetectDatabases(ctx context.Context) ([]DBEngine, error) {
	out, _ := h.Run(ctx, `p() { pgrep -x "$1" >/dev/null 2>&1; }; `+
		`command -v psql >/dev/null && (p postgres || p postmaster) && echo "postgres $(psql --version 2>/dev/null | awk '{print $3}')"; `+
		`command -v mysql >/dev/null && (p mysqld || p mariadbd) && echo "mysql $(mysql --version 2>/dev/null | sed 's/^mysql *//' | cut -c1-40 | tr ' ' '_')"; `+
		`command -v redis-cli >/dev/null && p redis-server && echo "redis $(redis-cli --version 2>/dev/null | awk '{print $2}')"; `+
		`command -v mongosh >/dev/null && p mongod && echo "mongo $(mongod --version 2>/dev/null | head -n1 | awk '{print $3}')"; true`)
	var list []DBEngine
	names := map[string]string{DBPostgres: "PostgreSQL", DBMySQL: "MySQL / MariaDB", DBRedis: "Redis", DBMongo: "MongoDB"}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		e := DBEngine{Kind: f[0]}
		if len(f) > 1 {
			e.Version = strings.ReplaceAll(f[1], "_", " ")
		}
		e.Label = names[e.Kind] + " " + e.Version + " · на сервере"
		list = append(list, e)
	}
	if h.Has(ctx, "docker") {
		if d, err := h.GetDocker(ctx, false); err == nil {
			for _, c := range d.Containers {
				if c.State != "running" {
					continue
				}
				img := strings.ToLower(c.Image)
				kind := ""
				switch {
				case strings.Contains(img, "postgres") || strings.Contains(img, "timescale") || strings.Contains(img, "postgis"):
					kind = DBPostgres
				case strings.Contains(img, "mysql") || strings.Contains(img, "mariadb"):
					kind = DBMySQL
				case strings.Contains(img, "redis") || strings.Contains(img, "valkey") || strings.Contains(img, "keydb"):
					kind = DBRedis
				case strings.Contains(img, "mongo"):
					kind = DBMongo
				}
				if kind != "" {
					list = append(list, DBEngine{Kind: kind, Container: c.ID, DockerRoot: d.Root,
						Label: names[kind] + " · контейнер " + c.Name + " (" + c.Image + ")"})
				}
			}
		}
	}
	return list, nil
}

// client — команда CLI-клиента СУБД (выполняется внутри sh -c на сервере
// или в контейнере). args — уже экранированные аргументы.
func (e DBEngine) client(db string) string {
	switch e.Kind {
	case DBPostgres:
		if e.Container != "" {
			c := `psql -X -P pager=off -U "${POSTGRES_USER:-postgres}"`
			if db != "" {
				c += " -d " + Q(db)
			}
			return c
		}
		c := "psql -X -P pager=off"
		if db != "" {
			c += " -d " + Q(db)
		}
		return c
	case DBMySQL:
		c := "mysql"
		if e.Container != "" {
			c = `mysql -uroot -p"${MYSQL_ROOT_PASSWORD:-$MARIADB_ROOT_PASSWORD}"`
		}
		if db != "" {
			c += " -D " + Q(db)
		}
		return c
	case DBRedis:
		c := "redis-cli"
		if db != "" {
			c += " -n " + Q(db)
		}
		return c
	case DBMongo:
		c := "mongosh --quiet"
		if db != "" {
			c += " " + Q(db)
		}
		return c
	}
	return "false"
}

// run выполняет inner (sh) там, где живёт СУБД.
func (h *Host) dbRun(ctx context.Context, e DBEngine, inner string) (string, error) {
	if e.Container != "" {
		if !dockerRefRe.MatchString(e.Container) {
			return "", errors.New("недопустимый ID контейнера")
		}
		cmd := "docker exec -i " + e.Container + " sh -c " + Q(inner) + " 2>&1"
		if e.Kind == DBPostgres {
			cmd = "docker exec -i -u postgres " + e.Container + " sh -c " + Q(inner) + " 2>&1"
		}
		if e.DockerRoot {
			return h.Sudo(ctx, cmd)
		}
		return h.Run(ctx, cmd)
	}
	if e.Kind == DBPostgres {
		// psql от системного пользователя postgres (peer-аутентификация).
		return h.Sudo(ctx, "su postgres -s /bin/sh -c "+Q("cd /tmp; "+inner)+" 2>&1")
	}
	return h.Sudo(ctx, inner+" 2>&1")
}

// ListDatabases — базы СУБД с размерами.
func (h *Host) ListDatabases(ctx context.Context, e DBEngine) ([]DBInfo, error) {
	var inner string
	switch e.Kind {
	case DBPostgres:
		inner = e.client("") + ` -A -t -F '|' -c ` + Q("select datname, pg_database_size(datname) from pg_database where not datistemplate order by 1")
	case DBMySQL:
		inner = e.client("") + ` -B -N -e ` + Q("SELECT s.schema_name, COALESCE(SUM(t.data_length+t.index_length),0) FROM information_schema.schemata s "+
			"LEFT JOIN information_schema.tables t ON t.table_schema=s.schema_name GROUP BY s.schema_name ORDER BY 1")
	case DBRedis:
		inner = e.client("") + " INFO keyspace"
	case DBMongo:
		inner = e.client("") + ` --eval ` + Q(`db.adminCommand({listDatabases:1}).databases.forEach(d=>print(d.name+"|"+d.sizeOnDisk))`)
	default:
		return nil, errors.New("неизвестная СУБД")
	}
	out, err := h.dbRun(ctx, e, inner)
	if err != nil {
		if out != "" {
			return nil, errors.New(strings.TrimSpace(out))
		}
		return nil, err
	}
	var list []DBInfo
	for _, l := range lines(out) {
		switch e.Kind {
		case DBPostgres, DBMongo:
			n, s, ok := strings.Cut(l, "|")
			if ok {
				size, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
				list = append(list, DBInfo{Name: strings.TrimSpace(n), Size: uint64(size)})
			}
		case DBMySQL:
			f := strings.Split(l, "\t")
			if len(f) >= 2 && !strings.HasPrefix(l, "mysql:") {
				size, _ := strconv.ParseFloat(f[1], 64)
				list = append(list, DBInfo{Name: f[0], Size: uint64(size)})
			}
		case DBRedis: // db0:keys=12,expires=0,avg_ttl=0
			if strings.HasPrefix(l, "db") {
				n, note, _ := strings.Cut(l, ":")
				list = append(list, DBInfo{Name: strings.TrimPrefix(n, "db"), Note: note})
			}
		}
	}
	if e.Kind == DBRedis && len(list) == 0 {
		list = append(list, DBInfo{Name: "0", Note: "пусто"})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

var dangerSQL = regexp.MustCompile(`(?i)\b(drop|truncate|delete|alter|flushall|flushdb|del|unlink|dropdatabase|deletemany|remove)\b`)

// IsDangerQuery — запрос что-то удаляет/меняет схему: UI спросит подтверждение.
func IsDangerQuery(q string) bool { return dangerSQL.MatchString(q) }

// Query выполняет запрос (SQL, команду redis, выражение mongosh) и
// возвращает вывод клиента как есть.
func (h *Host) Query(ctx context.Context, e DBEngine, db, q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", errors.New("пустой запрос")
	}
	var inner string
	switch e.Kind {
	case DBPostgres:
		inner = e.client(db) + " -c " + Q(q)
	case DBMySQL:
		inner = e.client(db) + " -t -e " + Q(q)
	case DBRedis:
		// Команда redis: слова через пробел; кавычки — как в redis-cli.
		inner = e.client(db) + " " + q
		if strings.ContainsAny(q, ";&|`$<>\n") {
			return "", errors.New("в команде redis недопустимы символы ; & | ` $ < >")
		}
	case DBMongo:
		inner = e.client(db) + " --eval " + Q(q)
	default:
		return "", errors.New("неизвестная СУБД")
	}
	out, err := h.dbRun(ctx, e, inner)
	if err != nil && out != "" {
		return out, nil // ошибка клиента — уже в выводе
	}
	return out, err
}

// DumpDir — куда по умолчанию складывать дампы на сервере.
const DumpDir = "/var/backups/vanta"

// Dump снимает дамп базы в файл на сервере (gzip). Возвращает путь.
func (h *Host) Dump(ctx context.Context, e DBEngine, db string) (string, error) {
	if db == "" {
		return "", errors.New("не выбрана база")
	}
	stamp := time.Now().Format("20060102-150405")
	safe := regexp.MustCompile(`[^A-Za-z0-9_.-]`).ReplaceAllString(db, "_")
	var inner, ext string
	switch e.Kind {
	case DBPostgres:
		user := ""
		if e.Container != "" {
			user = ` -U "${POSTGRES_USER:-postgres}"`
		}
		inner, ext = "pg_dump"+user+" -d "+Q(db), ".sql.gz"
	case DBMySQL:
		auth := ""
		if e.Container != "" {
			auth = ` -uroot -p"${MYSQL_ROOT_PASSWORD:-$MARIADB_ROOT_PASSWORD}"`
		}
		inner, ext = "mysqldump"+auth+" --single-transaction --routines --triggers "+Q(db), ".sql.gz"
	case DBMongo:
		inner, ext = "mongodump --quiet --archive --db "+Q(db), ".archive.gz"
	case DBRedis:
		inner, ext = "redis-cli --rdb /tmp/vanta-dump.rdb >/dev/null && cat /tmp/vanta-dump.rdb; r=$?; rm -f /tmp/vanta-dump.rdb; exit $r", ".rdb.gz"
	default:
		return "", errors.New("неизвестная СУБД")
	}
	file := fmt.Sprintf("%s/%s-%s-%s%s", DumpDir, e.Kind, safe, stamp, ext)
	var src string
	switch {
	case e.Container != "" && e.Kind == DBPostgres:
		src = "docker exec -u postgres " + e.Container + " sh -c " + Q(inner)
	case e.Container != "":
		src = "docker exec " + e.Container + " sh -c " + Q(inner)
	case e.Kind == DBPostgres:
		src = "su postgres -s /bin/sh -c " + Q("cd /tmp; "+inner)
	default:
		src = inner
	}
	cmd := "set -o pipefail 2>/dev/null; mkdir -p " + DumpDir + " && chmod 700 " + DumpDir + " && " + src + " | gzip > " + Q(file) + " && ls -l " + Q(file)
	if _, err := h.Sudo(ctx, "if command -v bash >/dev/null; then bash -c "+Q(cmd)+"; else sh -c "+Q(cmd)+"; fi"); err != nil {
		return "", err
	}
	return file, nil
}
