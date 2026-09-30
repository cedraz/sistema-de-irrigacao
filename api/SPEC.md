# Spec — API + site (Go + MongoDB + nginx)

Três containers: **front** (nginx com o site), **api** (Go) e **mongo**.
Sem login, sem usuários — quem abre o endereço, controla.

---

## 1. Como as peças se encontram

O ESP32 fica na casa do pai; o servidor é o caseiro. Redes diferentes, então os
dois se encontram pela internet, num endereço só.

```
📱 celular ──┐                                    ┌─ /        → arquivos do site
             ├─ https://irrigacao.cedraz.dev ─▶ front (nginx) ─┼─ /api/*   → api:8080 ─▶ 🍃 mongo
🔌 ESP32 ────┘   (Cloudflare Tunnel)              └─ /ws/*    → api:8080
```

Só o **front** publica porta. A API e o Mongo só existem dentro da rede do
compose. Como site e API saem pelo mesmo endereço, não existe CORS.

**Duas regras que saem desse desenho:**

1. **O celular nunca fala com o ESP32.** Ele fala com a API, e a API **empurra**
   o comando pelo WebSocket que o ESP32 mantém aberto. Resposta instantânea, e a
   conexão viva é a própria detecção de "aparelho online".
2. **O ESP32 só abre conexão, nunca recebe.** Por isso funciona em qualquer rede
   sem mexer no roteador da casa dele.

### Quem executa o horário programado

**A API.** Um ticker de 20s olha as programações e manda abrir na hora certa.

O ESP32 poderia rodar a agenda sozinho com NTP — sobreviveria a queda de
internet — mas isso exige guardar e interpretar a programação dentro da placa. A
escolha aqui foi a simples. O preço: **se o servidor estiver fora do ar às 07:00,
aquela rega não acontece.** A da noite acontece. Planta sobrevive; é um preço
aceitável enquanto a alternativa é firmware três vezes maior.

> Se um dia a internet da casa virar problema de verdade, o caminho é o ESP32
> baixar a agenda e executar com NTP. A API não muda; só o firmware.

---

## 2. Escopo

**Tem:**
- Ligar/desligar a válvula de qualquer lugar, com duração
- Programar horários: hora, duração e dias da semana
- Ligar/desligar cada horário sem apagar
- Histórico das regas (quando começou, quando terminou, se foi manual ou agenda)
- Site em HTML puro, feito pra celular

**Não tem (de propósito):**
- Login, usuários, senha — pedido explícito
- Sensor de umidade, previsão do tempo, múltiplas válvulas
- Migrations (Mongo não tem schema fixo)

---

## 3. Stack

| Peça | Escolha | Por quê |
|---|---|---|
| HTTP | `net/http` da stdlib | Go 1.22+ já roteia por método e path. Gin/Chi não acrescentam nada. |
| WebSocket | `github.com/coder/websocket` | A stdlib não tem WS. Menor biblioteca e a única que respeita `context`. |
| Banco | `go.mongodb.org/mongo-driver/v2` | Pedido. E encaixa: a programação é um documento, não uma tabela. |
| Site | HTML + CSS + JS na unha, servido por nginx | Nenhum build, nenhum `npm install`. O nginx também repassa `/api` e `/ws` pra API. |
| Config | `os.Getenv` | 5 variáveis. |
| Fuso | `_ "time/tzdata"` | Embute o banco de fusos no binário — sem isso a imagem enxuta não conhece `America/Sao_Paulo` e a rega sai na hora errada. |

Dependências: **2**.

---

## 4. Estrutura

```
sistema-de-irrigacao/
├── Makefile                  # make up, make publicar TAG=v1 ...
├── compose.yaml              # desenvolvimento: builda do código
├── .env                      # DEVICE_TOKEN
│
├── api/                      # imagem icarocedraz/sistema-de-irrigacao:api-*
│   ├── Dockerfile            # compila nativo no Mac, gera binário x86
│   ├── cmd/api/main.go       # env, mongo, agendador, ListenAndServe
│   └── internal/
│       ├── store/store.go    # TUDO que é Mongo. Não sabe o que é HTTP.
│       └── api/
│           ├── router.go     # rotas + handlers + ajudantes de JSON
│           ├── valvula.go    # Abrir/Fechar + agendador  ← o miolo
│           └── dispositivo.go# WebSocket do ESP32
│
├── web/                      # imagem icarocedraz/sistema-de-irrigacao:front-*
│   ├── Dockerfile
│   ├── nginx.conf            # site + proxy de /api e /ws
│   ├── index.html
│   ├── style.css
│   └── app.js
│
├── deploy/                   # o que vai pro servidor — e SÓ isto
│   ├── docker-compose.yml    # baixa as imagens do Docker Hub
│   └── .env.example
│
└── firmware/irrigacao/irrigacao.ino
```

Dentro da API: `api/` → `store/`. Nunca o contrário.

### Três detalhes do `nginx.conf` que evitam dor de cabeça

| Linha | Sem ela |
|---|---|
| `resolver 127.0.0.11` + `set $api` | o nginx guarda o IP da API pra sempre; atualizou a API, o site dá 502 |
| `proxy_read_timeout 1h` no `/ws/` | o padrão é 60s — a conexão do ESP32 cairia a cada minuto |
| `Cache-Control: no-cache` | depois de atualizar o site, o celular e a Cloudflare servem a versão velha por horas |

---

## 5. Dados (MongoDB)

Duas coleções, sem índice nenhum por enquanto — são dezenas de documentos.

**`programacoes`**
```json
{ "_id": ObjectId, "hora": "07:00", "duracao": 10, "dias": [1,2,3,4,5], "ativo": true }
```
`dias`: 0 = domingo … 6 = sábado. "Todo dia" é `[0,1,2,3,4,5,6]`.

**`historico`**
```json
{ "_id": ObjectId, "inicio": ISODate, "fim": ISODate|null, "origem": "manual"|"agenda" }
```
`fim: null` significa "ainda aberta". Duração é `fim - inicio` na hora de exibir,
nunca um campo guardado — campo calculado é dado duplicado esperando pra ficar
errado.

---

## 6. Endpoints

| Método | Rota | O quê |
|---|---|---|
| GET | `/` | o site |
| GET | `/api/estado` | `{aberta, confirmou, online, desde, fecha_em, proxima}` |
| POST | `/api/valvula` | `{abrir: bool, minutos: int}` |
| GET | `/api/programacoes` | lista |
| POST | `/api/programacoes` | cria |
| PUT | `/api/programacoes/{id}` | altera (inclusive o liga/desliga) |
| DELETE | `/api/programacoes/{id}` | apaga |
| GET | `/api/historico?limite=20` | últimas regas |
| GET | `/ws/dispositivo` | WebSocket do ESP32 (`X-Device-Token`) |

`aberta` é o que a API mandou; `confirmou` é o que o ESP32 respondeu que fez de
verdade. `online` é ter conexão WebSocket viva — não é chute por tempo.

### Protocolo com o ESP32

```
API   → ESP32:  {"abrir": true, "segundos": 600}
ESP32 → API:    {"aberta": true}
```

Ao conectar, a API manda o estado atual na hora. É a ressincronização: sem isso
o ESP32 volta de uma queda achando que está fechado enquanto a API acha que está
aberta.

---

## 7. Segurança — três camadas contra alagamento

A pior falha possível aqui é a válvula ficar aberta sozinha. Por isso a defesa é
repetida em três lugares independentes:

| Onde | O que faz |
|---|---|
| **Firmware** | fecha ao perder a conexão WebSocket (heartbeat de 15s) |
| **Firmware** | teto absoluto de 30 min, aconteça o que acontecer |
| **API** | ping a cada 20s; sem resposta em 10s, derruba a conexão e o site mostra "aparelho desligado" |
| **API** | manda fechar quando o prazo da rega acaba |
| **API** | fecha a válvula ao receber SIGTERM, antes de morrer |

E o teto de `maxMinutos = 120` na API: um dedo escorregando e digitando 600 em
vez de 60 não vira enchente.

No boot e em qualquer reset o pino do relé fica em `INPUT` — válvula fechada.

### O que "sem senha" significa

Foi escolha sua, e faz sentido: senha é barreira pro seu pai. Mas vale saber o
tamanho exato: **quem souber o endereço abre a válvula.** Não tem tentativa de
adivinhar senha, não tem nada a roubar — o pior caso é alguém molhar o jardim.

O ESP32 tem token (`DEVICE_TOKEN`) porque ele é a peça que não pode ser
imitada. O site não tem.

Se um dia incomodar, o mais barato sem atrapalhar seu pai é servir o site num
caminho secreto (`/j7k2m9/`) e deixar o atalho no celular dele — continua sem
senha pra digitar.

---

## 8. Rodar e publicar

**Na sua máquina:**

```bash
cp .env.example .env
sed -i '' "s|^DEVICE_TOKEN=.*|DEVICE_TOKEN=$(openssl rand -hex 32)|" .env
make up          # http://localhost:8080
```

**Publicar no Docker Hub:**

```bash
docker login
make publicar TAG=v1
```

Gera `api-v1` + `api-latest` e `front-v1` + `front-latest`, sempre pra
`linux/amd64` — o servidor é x86 e o Mac é ARM.

**No servidor:** só `deploy/docker-compose.yml` e o `.env`. Ver o README da raiz.

---

## 9. Quando complicar

| Sinal | O que fazer |
|---|---|
| Internet da casa cai demais | ESP32 baixa a agenda e executa com NTP |
| Segunda válvula | `dispositivos` como coleção, `device_id` nas rotas, hub de conexões |
| Quer saber se choveu | sensor de umidade mandando leitura pelo mesmo WebSocket |
| Histórico ficou gigante | índice em `inicio` e TTL pra apagar o que é velho |
