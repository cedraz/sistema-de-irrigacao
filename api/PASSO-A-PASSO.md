# Construindo a API em Go — do zero

> ⚠️ **Este guia descreve o desenho ANTIGO: Postgres, usuários com senha e
> migrations com goose.** O projeto mudou pra MongoDB, sem login. O código que
> está rodando hoje é o de [SPEC.md](SPEC.md) — use aquele como referência.
>
> O que continua valendo aqui: a **seção 0** (Go pra quem vem de TypeScript,
> receptor, `defer`, ponteiros) e a seção do **Makefile**. Essas não dependem de
> banco nenhum.

Guia pra quem **nunca escreveu Go**. Segue a [SPEC.md](SPEC.md); aqui é a mão na
massa: comandos, arquivos e templates.

Cada passo termina com **algo que você roda e vê funcionando**. Não pule pro
próximo antes disso — em Go o compilador reclama cedo, e é muito mais fácil achar
o erro em 20 linhas novas do que em 400.

---

## 0. Go pra quem vem de TypeScript

Você vai reconhecer quase tudo. As diferenças que importam:

| TypeScript | Go |
|---|---|
| `package.json` | `go.mod` |
| `npm install x` | `go get x` |
| `npm run dev` | `go run ./cmd/api` |
| `const nome = "a"` | `nome := "a"` (tipo inferido) |
| `let n: number = 1` | `var n int = 1` ou `n := 1` |
| `function f(a: string): number` | `func f(a string) int` — **o tipo vem depois do nome** |
| `try/catch` | não existe |
| `async/await` | não existe (tudo é síncrono; concorrência é `go`) |
| `Promise.all` | goroutines + canais |
| `null` / `undefined` | `nil` |
| `export function` | `func Maiuscula()` — **maiúscula = público** |
| `interface` | `interface` (mas implementada implicitamente) |
| `class Foo { bar() {} }` | `type Foo struct{}` + `func (f Foo) Bar() {}` |

### As 5 coisas que você precisa entender antes de escrever a primeira linha

**1. Erro é valor de retorno, não exceção.**

```go
usuario, err := s.store.UserByName(ctx, "cedraz")
if err != nil {
    return err          // ou trata
}
// daqui pra baixo, usuario é válido
```

Esse `if err != nil` vai aparecer 40 vezes no projeto. É feio e é de propósito:
todo caminho de erro fica visível, nada some num `catch` genérico lá em cima.

**2. Maiúscula é o `export`.**

`func Hash()` é visível de fora do pacote. `func hash()` só dentro do arquivo/pacote.
Mesma coisa pra tipos e campos de struct. Não existe `private`/`public`.

**3. `:=` cria, `=` atribui.**

```go
n := 1      // cria n
n = 2       // muda n
n := 3      // ERRO: já existe
```

**4. Método é função com "receptor" — o receptor é o `this`, escrito na mão.**

Em TypeScript você escreveria:

```ts
class Store {
  pool: Pool;
  ping() { return this.pool.ping(); }   // "this" aparece do nada
}
```

Go não tem classe. Você declara a struct (só os dados) e **pendura** funções nela
declarando um parâmetro extra **antes** do nome da função. Esse parâmetro é o
receptor, e ele é o `this` — só que explícito e com o nome que você quiser:

```go
type Store struct {          // só os dados, nenhum método aqui dentro
    pool *pgxpool.Pool
}

//    ┌─ receptor: é ISTO que faz a função virar "método do Store".
//    │  "s" é o que em TS seria "this" — você escolhe o nome.
//    ↓
func (s *Store) Ping(ctx context.Context) error {
    return s.pool.Ping(ctx)
}
```

Quando você chama `store.Ping(ctx)`, o Go passa `store` como `s`. Não é mágica —
é literalmente um parâmetro. Tanto que estas duas linhas são a mesma coisa:

```go
c.SomaNoOriginal()                  // jeito normal
(*Contador).SomaNoOriginal(&c)      // o que acontece por baixo (Go válido!)
```

O ganho em relação ao `this` do JavaScript: como é um parâmetro comum, **não
existe perder o `this`**. Nada de `.bind(this)`, nada de arrow function pra não
quebrar o contexto. O receptor sempre é o que você acha que é.

### E o `*`?

Aí está a única pegadinha. Sem `*` o método recebe uma **cópia**:

```go
type Contador struct{ n int }

func (c Contador) SomaNaCopia()     { c.n++ }   // mexe numa cópia — some
func (c *Contador) SomaNoOriginal() { c.n++ }   // mexe no original — fica
```

Rodando de verdade:

```
2x SomaNaCopia()     -> n = 0    (as somas se perderam, sem erro nenhum)
2x SomaNoOriginal()  -> n = 2
```

Repare que o primeiro **não dá erro**, só não faz nada. É o jeito mais silencioso
de perder uma tarde.

> **Regra prática pra este projeto: sempre use `*`.** `*Store`, `*Server`,
> `*device`. O `Store` guarda o pool de conexões — todo mundo tem que estar
> mexendo no *mesmo* Store, não em cópias dele.

**5. `context.Context` é o primeiro parâmetro de quase tudo.**

É o "AbortController do Go": carrega o cancelamento. Quando o celular fecha a
conexão, o `ctx` daquele request é cancelado e a query no Postgres é abortada
junto, sozinha. Você só precisa **repassar o `ctx` pra frente**.

**6. `defer` é o `finally`, escrito junto do que ele limpa.**

Em TypeScript, pra garantir a limpeza você embrulha tudo:

```ts
function f() {
  const t = setInterval(tick, 20000);
  try {
    // ... 20 caminhos diferentes de return aqui no meio
  } finally {
    clearInterval(t);        // longe, lá embaixo
  }
}
```

Go não tem `try/finally`. Tem `defer`: **agenda** a linha pra rodar quando a
função terminar, não importa por qual `return` ela saia (nem se der pânico).

```go
tick := time.NewTicker(20 * time.Second)
defer tick.Stop()          // ← a limpeza fica GRUDADA na criação
```

A vantagem não é a economia de linhas, é a **distância**: a limpeza fica uma
linha abaixo da criação, não 40 linhas depois. Você não esquece porque está
olhando pra ela.

No `device.go` do projeto o handler tem 5 caminhos de saída (ping falhou,
contexto cancelado, erro de escrita...). Sem `defer` você repetiria
`tick.Stop()`, `c.CloseNow()` e `s.dev.remove(c)` **antes de cada `return`** — e
esqueceria em um deles. É assim que nasce vazamento de conexão.

### As duas regras que pegam todo mundo

**Ordem é invertida (LIFO)** — o último `defer` declarado roda primeiro:

```
[1] corpo da função roda antes de qualquer defer
[2] este foi declarado primeiro, roda por último
[3] este defer foi declarado por último, roda primeiro
```

Faz sentido quando você pensa em recursos empilhados: abriu banco → abriu
transação → fecha transação → fecha banco.

**Os argumentos congelam na hora do `defer`**, não na hora que ele roda:

```go
n := 1
defer fmt.Println("defer viu n =", n)   // o 1 é capturado AQUI
n = 99
fmt.Println("no fim n =", n)
```

```
no fim n = 99
defer viu n = 1        ← congelou o valor antigo
```

> ⚠️ **`defer` dentro de `for` não roda no fim da volta — roda no fim da
> FUNÇÃO.** Um `defer rows.Close()` dentro de um loop de 1000 voltas segura 1000
> conexões abertas até a função acabar. Se precisar disso, extraia o corpo do
> loop pra uma função própria.

> Formate sempre com `gofmt -w .` (ou deixe o editor fazer). Go não tem debate de
> estilo: existe um formato certo e o resto é errado.

---

## 1. Instalar as ferramentas

Só o Go precisa ser instalado na máquina. O Postgres roda em container.

```bash
brew install go
go version     # precisa ser 1.22 ou maior — abaixo disso o roteador não existe
```

O `make` já vem no macOS. O CLI do goose vem depois, com `make tools`.

### Postgres via Docker

Crie o `api/compose.yaml`:

```yaml
services:
  db:
    image: postgres:17-alpine
    container_name: irrigacao-db
    restart: unless-stopped
    environment:
      POSTGRES_USER: irrigacao
      POSTGRES_PASSWORD: irrigacao
      POSTGRES_DB: irrigacao
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U irrigacao -d irrigacao"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  pgdata:
```

```bash
cd api
make db-up
docker compose ps          # espere ficar "healthy"
```

O banco sobe **vazio**. Quem cria as tabelas é o goose (passo 3), não o container.

✅ Teste:

```bash
psql "postgres://irrigacao:irrigacao@localhost:5432/irrigacao?sslmode=disable" -c "select 1"
```

> Não tem `psql` na máquina? Use o de dentro do container:
> `docker compose exec db psql -U irrigacao -d irrigacao -c "\dt"`

### Comandos do dia a dia

| Comando | O que faz |
|---|---|
| `make db-up` | sobe o banco |
| `make db-down` | para (os **dados continuam**) |
| `make db-reset` | para e **APAGA os dados**, sobe zerado |
| `make db-psql` | abre o psql |
| `docker compose logs -f db` | acompanha o log |

> **Por que Docker e não `brew install postgresql`?** Porque a versão fica fixada
> no arquivo, `docker compose down -v` desfaz tudo sem deixar sujeira na máquina,
> e o mesmo `compose.yaml` sobe igualzinho no seu servidor de casa depois. Com o
> brew você ganha um serviço rodando pra sempre no seu Mac e uma dor de cabeça no
> dia do upgrade de versão.

### O Makefile

Go **não tem `scripts` no `package.json`**. Não existe `go run dev`. O `Makefile`
preenche esse buraco: é uma tabela de receitas com nome.

```makefile
run: ## sobe a API (lê o .env sozinho)
	go run ./cmd/api
```

`make run` procura a receita `run` e executa a linha de baixo. É só isso — um
atalho. O ganho aparece quando o comando é feio:

```bash
# sem make
GOOSE_DRIVER=postgres GOOSE_DBSTRING=postgres://irrigacao:irrigacao@localhost:5432/irrigacao?sslmode=disable GOOSE_MIGRATION_DIR=./migrations goose up

# com make
make migrate-up
```

Quatro coisas do `Makefile` deste projeto que vale entender:

| Trecho | O que faz |
|---|---|
| `-include .env` + `export` | carrega o `.env` e repassa pros comandos — **é isso que faz `make run` funcionar sem `source .env`** |
| `export GOOSE_DBSTRING := $(DATABASE_URL)` | variável montada a partir de outra |
| `check: fmt vet test` | alvo que depende de outros: roda os três em ordem e para no primeiro que falhar |
| `.PHONY: run build ...` | avisa que esses nomes **não são arquivos** — sem isso, se existisse um arquivo chamado `test`, `make test` diria "nothing to do" |

```bash
make help      # lista tudo que dá pra fazer
```

> ⚠️ **A pegadinha nº 1 do Makefile: a indentação é TAB, nunca espaço.** Se o
> editor converter, o make cospe `missing separator` e você perde 20 minutos.
> Todo mundo passa por isso uma vez.

---

## 2. Criar o módulo e baixar as bibliotecas

```bash
cd api
go mod init github.com/cedraz/sistema-de-irrigacao/api
```

> Esse nome é o **identificador** do módulo, não uma URL que precisa existir.
> Ele vira o prefixo dos seus imports. Se um dia publicar no GitHub nesse
> caminho, já está certo.

```bash
go get github.com/jackc/pgx/v5
go get golang.org/x/crypto/bcrypt
go get github.com/coder/websocket
go get github.com/pressly/goose/v3
```

Quatro dependências. Só isso o projeto inteiro precisa.

```bash
mkdir -p cmd/api internal/store internal/auth internal/api migrations
make tools        # instala o binário do goose (o CLI, separado da biblioteca)
```

> A biblioteca (`go get`) é o que a API usa pra aplicar migration ao subir. O CLI
> (`make tools`) é o que **você** usa pra criar e rodar migration na mão. São a
> mesma coisa em dois formatos.

✅ `cat go.mod` deve listar as três em `require`.

---

## 3. Banco: migrations com goose

Nada de um `schema.sql` que você aplica na mão. Cada mudança de schema é **um
arquivo `.sql`** numerado, e o goose sabe quais já rodaram.

```bash
make migrate-create name=init
```

Ele cria `migrations/20260902120000_init.sql` (o número é a data/hora). Preencha:

```sql
-- +goose Up
-- +goose StatementBegin
create table users (
  id         bigserial primary key,
  name       text not null unique,
  password   text not null,
  created_at timestamptz not null default now()
);

create table sessions (
  token      text primary key,
  user_id    bigint not null references users(id) on delete cascade,
  created_at timestamptz not null default now()
);

create table irrigations (
  id         bigserial primary key,
  started_at timestamptz not null default now(),
  ended_at   timestamptz,
  source     text not null,
  user_id    bigint references users(id)
);

-- só pode existir uma rega aberta por vez
create unique index one_open_irrigation
  on irrigations ((ended_at is null)) where ended_at is null;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists irrigations;
drop table if exists sessions;
drop table if exists users;
-- +goose StatementEnd
```

Aqueles comentários **não são comentários** — são diretivas que o goose lê.
`Up` aplica, `Down` desfaz.

```bash
make migrate-up
make migrate-status
```

✅ Teste:

```bash
psql "$DATABASE_URL" -c "\dt"
```

Deve listar as 3 tabelas **mais** a `goose_db_version` — é nela que o goose anota
o que já rodou. Por isso `make migrate-up` duas vezes não faz nada na segunda:

```
goose: no migrations to run. current version: 20260902120000
```

Aquele índice único é a trava de concorrência: dois "ligar" ao mesmo tempo não
criam duas regas abertas — **o banco recusa a segunda**. Sem mutex nenhum no Go.

### Embutindo as migrations no binário

Crie `migrations/embed.go`:

```go
// Package migrations guarda os .sql e os embute no binário.
//
// O //go:embed só enxerga arquivos DESTA pasta pra baixo — por isso este
// arquivo mora aqui e não em cmd/api/.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

`//go:embed` (sem espaço depois do `//`, é uma diretiva do compilador) coloca os
arquivos **dentro** do executável. No servidor você copia um binário só e
reinicia — nada de lembrar de levar a pasta `migrations/` junto. O `main.go` do
passo 8 usa isso.

### Fluxo de todo dia

| Situação | Comando |
|---|---|
| Preciso mudar o schema | `make migrate-create name=add_umidade` |
| Aplicar o que está pendente | `make migrate-up` |
| Errei a última | `make migrate-down`, corrige o arquivo, `make migrate-up` |
| Quero começar do zero | `make db-reset && make migrate-up` |

> ⚠️ **Migration que já foi pra produção não se edita, nunca.** O goose já anotou
> o número dela como aplicada e não vai rodar de novo. Precisa mudar? Cria uma
> nova migration por cima.

### `.env.example`

```bash
PORT=8080
DATABASE_URL=postgres://irrigacao:irrigacao@localhost:5432/irrigacao?sslmode=disable
DEVICE_TOKEN=troque-isto
ALLOW_REGISTER=true
MAX_OPEN_MINUTES=10
```

```bash
cp .env.example .env
sed -i '' "s|^DEVICE_TOKEN=.*|DEVICE_TOKEN=$(openssl rand -hex 32)|" .env
echo ".env" >> ../.gitignore
```

O `Makefile` lê o `.env` sozinho (`-include .env` + `export` no topo dele), então
`make run` já sobe com tudo no lugar. Sem ele você teria que digitar
`set -a && source .env && set +a && go run ./cmd/api` toda vez.

---

## 4. Onde ficam controller, service e repository

Você pediu nesses nomes. O mapeamento honesto:

| Nome que você conhece | Aqui | Por quê |
|---|---|---|
| **Repository** | `internal/store/` | ✅ existe, igualzinho |
| **Controller** | `internal/api/` | ✅ existe, chama de *handler* |
| **Service** | **não existe** | ⚠️ leia abaixo |

**Go não tem camada de service neste projeto, e não é esquecimento.** Uma service
existe pra guardar regra de negócio que não é nem HTTP nem SQL. Aqui a regra de
negócio inteira é:

- "abrir = inserir linha" → o banco já garante que só existe uma aberta
- "fechar = preencher `ended_at`" → uma linha de SQL
- "avisar o ESP32" → 3 linhas no handler

Criar `internal/service/valve.go` só pra repassar chamadas do handler pro store
adiciona um arquivo, uma indireção e zero comportamento. **Quando** aparecer
lógica de verdade — checar horário permitido, limite de regas por dia, calcular
consumo de água — aí a service nasce sozinha e você move a lógica pra lá.

O único pedaço que já é "quase service" é o `push` pro ESP32, e ele mora em
`internal/api/device.go` porque é quem é dono da conexão WebSocket.

```
cmd/api  ──▶  internal/api  ──▶  internal/store  ──▶  🐘
            (controller)          (repository)
                  │
                  └──▶  internal/auth   (só hash e token, não conhece o resto)
```

**As setas nunca voltam.** Se você precisar importar `api` dentro de `store`,
parou tudo: a função está no pacote errado.

---

## 5. Repository — `internal/store/`

### `internal/store/store.go`

```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store é o dono da conexão com o Postgres. Todo SQL do projeto passa por aqui.
type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// pgxpool.New não conecta de verdade: só o Ping descobre se o banco existe.
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
```

### `internal/store/users.go` — **este é O template de repository**

Todo repository do projeto segue este formato. Leia com calma, os outros são
repetição disto.

```go
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type User struct {
	ID       int64
	Name     string
	Password string // hash bcrypt — a senha em texto nunca chega aqui
}

// ErrNotFound deixa o handler decidir o status HTTP. O store não conhece HTTP.
var ErrNotFound = errors.New("não encontrado")

// IsDuplicate diz se o erro é violação de índice único (nome já cadastrado).
// 23505 é o código do Postgres pra unique_violation.
func IsDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Store) CreateUser(ctx context.Context, name, hash string) (User, error) {
	u := User{Name: name, Password: hash}
	err := s.pool.QueryRow(ctx,
		`insert into users (name, password) values ($1, $2) returning id`,
		name, hash,
	).Scan(&u.ID)
	return u, err
}

func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`select id, name, password from users where name = $1`,
		name,
	).Scan(&u.ID, &u.Name, &u.Password)

	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}
```

O que aprender daqui:

- **`$1`, `$2` são placeholders do Postgres.** Nunca monte SQL com `+` ou
  `fmt.Sprintf` — é assim que nasce SQL injection.
- **`.Scan(&u.ID)`** copia a coluna pra dentro da variável. O `&` passa o
  endereço, senão o Scan escreveria numa cópia e você receberia zero.
- **A ordem do `Scan` tem que bater com a ordem do `select`.** O compilador não
  te protege disso. É o erro nº 1 de quem começa.

### `internal/store/sessions.go`

```go
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateSession(ctx context.Context, userID int64, token string) error {
	_, err := s.pool.Exec(ctx,
		`insert into sessions (token, user_id) values ($1, $2)`,
		token, userID,
	)
	return err
}

func (s *Store) UserBySession(ctx context.Context, token string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`select u.id, u.name, u.password
		   from sessions s join users u on u.id = s.user_id
		  where s.token = $1`,
		token,
	).Scan(&u.ID, &u.Name, &u.Password)

	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}
```

`Exec` quando não volta linha, `QueryRow` quando volta uma, `Query` quando volta
várias.

### `internal/store/irrigations.go` — **você escreve este**

É a repetição do padrão acima. Aqui está tudo que você precisa; o SQL está
pronto, falta traduzir pro formato do `users.go`.

```go
type Irrigation struct {
	ID        int64
	StartedAt time.Time
	EndedAt   *time.Time // ponteiro: nil = ainda aberta
	Seconds   int
	Source    string
}
```

> **Por que `*time.Time` e não `time.Time`?** Porque `ended_at` pode ser `NULL`
> no banco, e `time.Time` não tem como representar "vazio". Ponteiro pode ser
> `nil`. Mesma ideia do `Date | null` do TypeScript.

| Método | SQL |
|---|---|
| `Open(ctx, source string, userID int64) (Irrigation, error)` | `insert into irrigations (source, user_id) values ($1, $2) returning id, started_at` |
| `CloseCurrent(ctx) error` | `update irrigations set ended_at = now() where ended_at is null` |
| `Current(ctx) (*Irrigation, error)` | `select id, started_at, source from irrigations where ended_at is null` → devolve `nil, nil` quando `pgx.ErrNoRows` (não é erro: significa "fechada") |
| `List(ctx, limit int) ([]Irrigation, error)` | `select id, started_at, ended_at, extract(epoch from (coalesce(ended_at, now()) - started_at))::int, source from irrigations order by started_at desc limit $1` |
| `CloseStale(ctx, max time.Duration) (int, error)` | `update irrigations set ended_at = now() where ended_at is null and started_at < now() - $1::interval`, passando `max.String()` → devolve `res.RowsAffected()` |

> Parece que não deveria funcionar, mas funciona: `time.Duration.String()` gera
> `"10m0s"` e o parser de `interval` do Postgres entende esse formato. Testado.

> **Por que `CloseCurrent` e não `Close`?** Porque `Store.Close()` já existe
> (fecha o pool do banco) e **Go não tem sobrecarga de método**: dois métodos com
> o mesmo nome no mesmo tipo não compilam, mesmo com parâmetros diferentes.

O `List` é o único diferente, porque volta várias linhas. O pgx v5 tem um atalho
que **elimina o erro de ordem do `Scan`** — ele casa coluna com campo pelo nome:

```go
rows, err := s.pool.Query(ctx, `select id, started_at, ended_at, ... `, limit)
if err != nil {
	return nil, err
}
return pgx.CollectRows(rows, pgx.RowToStructByName[Irrigation])
```

Pra isso os campos precisam de tag: `StartedAt time.Time `db:"started_at"``.
É o mesmo que a `sqlx` faz (a biblioteca que vocês usam na doji), só que já vem
dentro do pgx, sem dependência a mais.

Escrevendo na mão — que vale fazer uma vez, pra entender o que acontece por baixo:

```go
rows, err := s.pool.Query(ctx, `select ...`, limit)
if err != nil {
	return nil, err
}
defer rows.Close()          // ⚠️ sem isso a conexão vaza do pool

lista := []Irrigation{}
for rows.Next() {
	var it Irrigation
	if err := rows.Scan(&it.ID, &it.StartedAt, &it.EndedAt, &it.Seconds, &it.Source); err != nil {
		return nil, err
	}
	lista = append(lista, it)
}
return lista, rows.Err()    // ⚠️ erro no meio do loop só aparece aqui
```

> Devolva `[]Irrigation{}` e não `nil` quando não tem nada: `nil` vira `null` no
> JSON e o `FlatList` do app quebra. Slice vazio vira `[]`.

✅ Teste do passo: `go build ./...` sem erro.

---

## 6. `internal/auth/auth.go`

Pacote mais isolado do projeto: entra string, sai string. **Não importa nada do
projeto** — é por isso que o middleware de sessão *não* mora aqui (ele precisa do
banco, e `auth` não pode conhecer o `store`).

```go
package auth

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// Hash de uma senha qualquer. Serve pra gastar o mesmo tempo de CPU quando o
// usuário NÃO existe — senão o tempo de resposta entrega quais nomes existem.
var hashFalso, _ = bcrypt.GenerateFromPassword([]byte("nao-existe"), bcrypt.DefaultCost)

func Hash(senha string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(senha), bcrypt.DefaultCost)
	return string(b), err
}

// Check compara a senha com o hash. Hash vazio (usuário inexistente) ainda gasta
// o tempo do bcrypt de propósito: é a defesa contra timing attack.
func Check(hash, senha string) bool {
	if hash == "" {
		bcrypt.CompareHashAndPassword(hashFalso, []byte(senha))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(senha)) == nil
}

// NewToken devolve 32 bytes aleatórios em hex (64 caracteres).
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand falhando = sistema quebrado, não dá pra seguir
	}
	return hex.EncodeToString(b)
}
```

> `bcrypt` tem limite de **72 bytes** na senha — o que passar disso é ignorado
> silenciosamente. Não é problema com senha normal, mas é bom saber.

---

## 7. Controller — `internal/api/`

### `internal/api/router.go`

O esqueleto: quem é o servidor, quais são as rotas, e os ajudantes que todo
handler usa.

```go
package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/cedraz/sistema-de-irrigacao/api/internal/store"
)

type Server struct {
	store         *store.Store
	dev           *device // a conexão do ESP32 (device.go)
	deviceToken   string
	allowRegister bool
}

func NewServer(st *store.Store, deviceToken string, allowRegister bool) *Server {
	return &Server{
		store:         st,
		dev:           &device{},
		deviceToken:   deviceToken,
		allowRegister: allowRegister,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Go 1.22+ aceita o método na própria rota. Antes disso precisava de Chi/Gin.
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("POST /login", s.handleLogin)

	// require = middleware de sessão
	mux.Handle("GET /valve", s.require(http.HandlerFunc(s.handleGetValve)))
	mux.Handle("POST /valve", s.require(http.HandlerFunc(s.handleSetValve)))
	mux.Handle("GET /logs", s.require(http.HandlerFunc(s.handleLogs)))

	// O ESP32 usa token fixo, não sessão: autenticação própria dentro do handler.
	mux.HandleFunc("GET /device/ws", s.handleDeviceWS)

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "banco fora do ar")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- middleware de sessão ---

// Chave privada pro context. Tipo próprio (e não string) pra ninguém de fora
// conseguir sobrescrever esse valor por acidente.
type chaveUsuario struct{}

func (s *Server) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "faltou o token")
			return
		}

		u, err := s.store.UserBySession(r.Context(), token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "sessão inválida")
			return
		}

		ctx := context.WithValue(r.Context(), chaveUsuario{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func usuarioDo(ctx context.Context) store.User {
	u, _ := ctx.Value(chaveUsuario{}).(store.User)
	return u
}

// --- ajudantes de JSON ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status) // ⚠️ tem que vir DEPOIS dos headers e ANTES do corpo
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("erro escrevendo resposta: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readJSON limita o corpo a 1MB: sem isso alguém manda um JSON de 2GB e derruba
// a API por falta de memória.
func readJSON(r *http.Request, dst any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
}
```

### `internal/api/users.go` — **este é O template de controller**

```go
package api

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/cedraz/sistema-de-irrigacao/api/internal/auth"
	"github.com/cedraz/sistema-de-irrigacao/api/internal/store"
)

// As tags `json:"name"` dizem como o campo se chama no JSON.
// Sem elas o Go usaria "Name" com maiúscula.
type credenciais struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.allowRegister {
		writeErr(w, http.StatusForbidden, "cadastro desativado")
		return
	}

	var in credenciais
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "json inválido")
		return
	}

	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 3 || len(in.Name) > 32 {
		writeErr(w, http.StatusBadRequest, "nome precisa ter de 3 a 32 caracteres")
		return
	}
	if len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "senha precisa ter no mínimo 8 caracteres")
		return
	}

	hash, err := auth.Hash(in.Password)
	if err != nil {
		log.Printf("hash: %v", err)
		writeErr(w, http.StatusInternalServerError, "erro ao criar usuário")
		return
	}

	u, err := s.store.CreateUser(r.Context(), in.Name, hash)
	if store.IsDuplicate(err) {
		writeErr(w, http.StatusConflict, "esse nome já existe")
		return
	}
	if err != nil {
		log.Printf("CreateUser: %v", err)
		writeErr(w, http.StatusInternalServerError, "erro ao criar usuário")
		return
	}

	s.novaSessao(w, r, u, http.StatusCreated)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credenciais
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "json inválido")
		return
	}

	u, err := s.store.UserByName(r.Context(), strings.TrimSpace(in.Name))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("UserByName: %v", err)
		writeErr(w, http.StatusInternalServerError, "erro ao entrar")
		return
	}

	// u.Password fica vazio se o usuário não existe. auth.Check gasta o tempo do
	// bcrypt mesmo assim, de propósito — e a mensagem é a MESMA nos dois casos,
	// pra não entregar quais nomes existem.
	if !auth.Check(u.Password, in.Password) {
		writeErr(w, http.StatusUnauthorized, "nome ou senha inválidos")
		return
	}

	s.novaSessao(w, r, u, http.StatusOK)
}

func (s *Server) novaSessao(w http.ResponseWriter, r *http.Request, u store.User, status int) {
	token := auth.NewToken()
	if err := s.store.CreateSession(r.Context(), u.ID, token); err != nil {
		log.Printf("CreateSession: %v", err)
		writeErr(w, http.StatusInternalServerError, "erro ao criar sessão")
		return
	}
	writeJSON(w, status, map[string]string{"token": token})
}
```

**O formato de todo handler, sempre o mesmo:**

1. decodifica o corpo → `400` se o JSON for inválido
2. valida os campos → `400` com mensagem útil
3. chama o store
4. traduz o erro do store pra status HTTP
5. `writeJSON` com o resultado

E a regra de ouro: **erro interno vai pro `log`, mensagem genérica vai pro
usuário.** Nunca devolva `err.Error()` na resposta — isso vaza nome de tabela,
estrutura do banco e caminho de arquivo pra quem estiver batendo na sua API.

### `internal/api/valve.go` — **você escreve este**

```go
type valveResp struct {
	On           bool   `json:"on"`
	Since        *string `json:"since"`
	DeviceOnline bool   `json:"device_online"`
}
```

- **`GET /valve`** → `s.store.Current(ctx)`; monta a resposta com
  `s.dev.online()` no `device_online`.
- **`POST /valve`** → lê `{"on": bool}`.
  - `on: true` → se `Current` já devolveu algo, **não insira de novo**: responda
    `200` com o estado atual (apertar duas vezes não pode quebrar). Senão
    `s.store.Open(ctx, "app", usuarioDo(ctx).ID)`.
    > Entre o `Current` e o `Open` cabe uma corrida: dois cliques ao mesmo tempo
    > passam os dois pelo `Current`. Quem perder leva `23505` do índice único —
    > trate com `store.IsDuplicate(err)` e responda `200`, não `500`. A válvula
    > já está aberta, que era o que o usuário queria.
  - `on: false` → `s.store.CloseCurrent(ctx)`.
  - **Nos dois casos, depois de gravar:** `s.dev.push(ctx, on)` — é essa linha
    que acende o relé. Sem ela, a API grava e o ESP32 nunca fica sabendo.
- **`GET /logs`** → `limit` do query string com `strconv.Atoi`, default 50, teto
  200. Converta `[]store.Irrigation` pra JSON com as tags `started_at`,
  `ended_at`, `seconds`, `source`.

### `internal/api/device.go` — o WebSocket

A parte mais difícil do projeto, então vai completa. Leia os comentários: cada um
marca um jeito de errar que dá trabalho pra descobrir depois.

```go
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// device guarda A conexão do ESP32.
// ponytail: um dispositivo, um ponteiro. Hub de conexões quando existir a
// segunda válvula, não antes.
type device struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

type estadoMsg struct {
	On bool `json:"on"`
}

func (d *device) online() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn != nil
}

// substitui troca a conexão ativa e fecha a anterior. O ESP32 reinicia e
// reconecta antes do TCP velho morrer — sem isso você acumula conexão zumbi e
// manda comando pra um socket morto.
func (d *device) substitui(c *websocket.Conn) {
	d.mu.Lock()
	antiga := d.conn
	d.conn = c
	d.mu.Unlock()

	if antiga != nil {
		antiga.Close(websocket.StatusNormalClosure, "outra conexão assumiu")
	}
}

func (d *device) remove(c *websocket.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == c { // só limpa se ainda for a MESMA conexão
		d.conn = nil
	}
}

// push manda o estado pro ESP32. Silencioso se ele estiver offline: quando
// reconectar, recebe o estado atual no handshake.
func (d *device) push(ctx context.Context, on bool) {
	// O mutex também serializa as escritas: dois POST /valve ao mesmo tempo
	// escrevendo no mesmo conn = pânico.
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := escreveEstado(ctx, d.conn, on); err != nil {
		log.Printf("push pro esp32 falhou: %v", err)
	}
}

func escreveEstado(ctx context.Context, c *websocket.Conn, on bool) error {
	b, err := json.Marshal(estadoMsg{On: on})
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, b)
}

func (s *Server) handleDeviceWS(w http.ResponseWriter, r *http.Request) {
	// ConstantTimeCompare em vez de ==: comparação que sai cedo vaza o token
	// pelo tempo de resposta.
	enviado := []byte(r.Header.Get("X-Device-Token"))
	esperado := []byte(s.deviceToken)
	if len(esperado) == 0 || subtle.ConstantTimeCompare(enviado, esperado) != 1 {
		writeErr(w, http.StatusUnauthorized, "token do dispositivo inválido")
		return
	}

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("accept ws: %v", err)
		return
	}
	defer c.CloseNow()

	// CloseRead põe uma goroutine lendo em segundo plano: é ela que responde os
	// pings e detecta a queda. Sem isso, o Ping abaixo trava esperando o pong.
	// O ctx devolvido é cancelado quando o ESP32 cai.
	ctx := c.CloseRead(r.Context())

	s.dev.substitui(c)
	defer s.dev.remove(c)

	// Ressincroniza NA HORA. O ESP32 pode ter perdido um comando enquanto estava
	// desconectado; sem este envio ele reconecta achando que está fechado
	// enquanto a API acha que está aberto.
	atual, err := s.store.Current(ctx)
	if err != nil {
		log.Printf("Current: %v", err)
		return
	}
	if err := escreveEstado(ctx, c, atual != nil); err != nil {
		log.Printf("estado inicial: %v", err)
		return
	}

	log.Println("esp32 conectou")
	defer log.Println("esp32 desconectou")

	// A Cloudflare derruba WebSocket ocioso por volta de 100s. O ping segura.
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				log.Printf("esp32 não respondeu ao ping: %v", err)
				return
			}
		}
	}
}

// FecharEsquecidas fecha rega aberta há tempo demais e avisa o ESP32.
// Rodada por um ticker no main: é o cinto de segurança do suspensório do
// firmware — se o ESP32 travar com a válvula aberta MAS mantiver a conexão,
// o watchdog dele não dispara e alguém precisa mandar fechar.
func (s *Server) FecharEsquecidas(ctx context.Context, max time.Duration) (int, error) {
	n, err := s.store.CloseStale(ctx, max)
	if err != nil || n == 0 {
		return n, err
	}
	s.dev.push(ctx, false)
	return n, nil
}
```

---

## 8. `cmd/api/main.go`

O único arquivo que conhece o mundo de fora: variáveis de ambiente, porta, e a
ordem em que as peças se ligam.

```go
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registra o driver "pgx" no database/sql
	"github.com/pressly/goose/v3"

	"github.com/cedraz/sistema-de-irrigacao/api/internal/api"
	"github.com/cedraz/sistema-de-irrigacao/api/internal/store"
	"github.com/cedraz/sistema-de-irrigacao/api/migrations"
)

// migrar aplica as migrations pendentes na subida da API.
//
// O goose fala database/sql e a API usa pgxpool — são dois jeitos diferentes de
// conversar com o mesmo Postgres. Por isso abrimos uma conexão separada só pra
// isso e fechamos em seguida.
func migrar(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS) // lê os .sql de DENTRO do binário
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, ".")
}

func env(chave, padrao string) string {
	if v := os.Getenv(chave); v != "" {
		return v
	}
	return padrao
}

func main() {
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")

	// Migrations ANTES de tudo: se o schema estiver velho, nada mais funciona.
	if err := migrar(dsn); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		log.Fatalf("banco: %v", err) // Fatalf = loga e encerra o processo
	}
	defer st.Close()

	token := os.Getenv("DEVICE_TOKEN")
	if token == "" {
		log.Fatal("DEVICE_TOKEN vazio — o ESP32 não teria como se autenticar")
	}

	srv := api.NewServer(st, token, env("ALLOW_REGISTER", "true") == "true")

	maxMin, _ := strconv.Atoi(env("MAX_OPEN_MINUTES", "10"))
	go func() { // "go" = roda em paralelo, sem travar o resto
		for range time.Tick(time.Minute) {
			n, err := srv.FecharEsquecidas(ctx, time.Duration(maxMin)*time.Minute)
			if err != nil {
				log.Printf("fechar esquecidas: %v", err)
			} else if n > 0 {
				log.Printf("fechei %d rega(s) por tempo máximo", n)
			}
		}
	}()

	h := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second, // proteção contra Slowloris
		// WriteTimeout fica ZERO de propósito: qualquer valor aqui mata a
		// conexão WebSocket do ESP32 no meio.
	}

	log.Printf("ouvindo em %s", h.Addr)
	log.Fatal(h.ListenAndServe())
}
```

---

## 9. Rodar e testar

```bash
make check     # fmt + vet + test
make run       # sobe a API, lendo o .env sozinho
```

> `make check` roda o `go vet`, que acha erros que **compilam mas estão errados**
> (tag de JSON malformada, `Printf` com argumento sobrando). Rode antes de
> commitar. E repare que `make run` não precisa de `source .env`: o `-include .env`
> no topo do Makefile já cuidou disso.

Em outro terminal:

```bash
curl -s localhost:8080/health

TOKEN=$(curl -s -XPOST localhost:8080/register \
  -H 'Content-Type: application/json' \
  -d '{"name":"cedraz","password":"uma-senha-boa"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')

curl -s localhost:8080/valve -H "Authorization: Bearer $TOKEN"
curl -s -XPOST localhost:8080/valve -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"on":true}'
curl -s localhost:8080/logs -H "Authorization: Bearer $TOKEN"
```

Confira que a senha virou hash de verdade:

```bash
make db-psql
# depois, dentro do psql:
select name, left(password, 7) from users;
```

✅ Tem que aparecer `$2a$10$` — nunca a senha em texto.

### Testar o WebSocket sem ESP32 nenhum

```bash
brew install websocat
websocat -H='X-Device-Token: SEU_TOKEN' ws://localhost:8080/device/ws
```

Deve imprimir `{"on":false}` assim que conectar. Em outro terminal mande
`POST /valve {"on":true}` e veja `{"on":true}` **aparecer sozinho** no websocat.
Mate o websocat e confira que `GET /valve` volta a dizer `device_online: false`.

Se isso funciona, o ESP32 vai funcionar.

---

## 10. Erros que você vai cometer (todo mundo comete)

| Erro | O que acontece | Conserto |
|---|---|---|
| `declared and not used` | não compila | Go proíbe variável sem uso. Apague ou use `_` |
| `imported and not used` | não compila | idem pros imports. `gofmt` não remove, o editor sim |
| Esqueceu `&` no `Scan` | erro em runtime | `Scan` precisa do endereço: `.Scan(&u.ID)` |
| Ordem do `Scan` ≠ ordem do `select` | valores trocados, sem erro | confira coluna por coluna |
| Esqueceu `rows.Close()` | pool esgota e a API trava depois de N requests | `defer rows.Close()` |
| `nil map` | pânico ao escrever | `make(map[string]int)` antes de usar |
| Devolveu slice `nil` | vira `null` no JSON e quebra o app | devolva `[]T{}` |
| `WriteTimeout` configurado | WebSocket cai sozinho a cada X segundos | deixe zero |
| `w.WriteHeader` antes do `Header().Set` | header ignorado | headers primeiro, status depois, corpo por último |
| Espaço em vez de TAB no Makefile | `missing separator` | indentação de receita é TAB, sempre |
| Editou migration que já rodou | mudança não aplica, sem erro | o goose já anotou o número; crie uma migration nova |
| `//go:embed` com espaço (`// go:embed`) | `FS` vazio em runtime | é diretiva do compilador, não comentário: sem espaço |

Quando não entender um erro, cole ele no `go doc`:

```bash
go doc net/http.ServeMux
go doc github.com/coder/websocket.Conn.Ping
```

---

## 11. Ordem de ataque

Não escreva tudo e rode no fim. Nesta ordem, testando cada uma:

0. `compose.yaml` + `Makefile` + primeira migration → `make db-up && make migrate-up`
1. `store.go` + `main.go` com só o `/health` → `make run` e `curl localhost:8080/health`
2. `users.go` (store) + `auth.go` + `users.go` (api) → `/register` e `/login`
3. `require` no router → `GET /valve` sem token deve dar `401`
4. `irrigations.go` + `valve.go` → ligar/desligar, conferindo no `psql`
5. `device.go` → testar com `websocat`
6. `/logs`
7. Cloudflare Tunnel ([SPEC.md, passo 8](SPEC.md#7-passo-a-passo))
8. Firmware

Do passo 1 ao 3 você aprende Go. Do 4 em diante é repetir o que já sabe.
