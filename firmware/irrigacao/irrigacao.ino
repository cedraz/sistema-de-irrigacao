// Irrigação — ESP32
//
// O ESP32 abre uma conexão WebSocket com a API e fica esperando comando.
// Ele NUNCA recebe conexão de fora: só abre. Por isso funciona em qualquer
// rede, sem mexer no roteador.
//
// O interruptor da caixa foi desligado do software de propósito (veja abaixo).
//
// Bibliotecas necessárias (Gerenciador de Bibliotecas do Arduino IDE):
//   - WebSockets          (por Markus Sattler / Links2004)
//   - Adafruit ST7735 and ST7789 Library
//   - Adafruit GFX Library

#include <Adafruit_GFX.h>
#include <Adafruit_ST7789.h>
#include <SPI.h>
#include <WebSocketsClient.h>
#include <WiFi.h>

// ─────────────────────────── configuração ───────────────────────────

const char *WIFI_SSID = "Lula_2024";
const char *WIFI_SENHA = "Bv_24025968";

// Endereço da API. Troque pelo seu domínio quando publicar o servidor.
const char *SERVIDOR = "irrigacao.cedraz.dev";
const int PORTA = 443;
const char *CAMINHO = "/ws/dispositivo";
const bool USAR_TLS = true; // false + porta 8080 pra testar na rede local

// Precisa ser IGUAL ao DEVICE_TOKEN do .env da API.
const char *TOKEN =
    "51ac9f8d36bf94d9fd9774e4da77e7313e0bf6811d09b73ff546c6de396d3cae";

// Teto absoluto de segurança. Mesmo que a API mande um número maluco, ou
// que a resposta se perca, a válvula fecha sozinha depois disto.
const unsigned long MAX_ABERTA_MS = 30UL * 60UL * 1000UL; // 30 minutos

// ─────────────────────────── pinos ───────────────────────────

#define PINO_RELE 15
// GPIO 13 tem o interruptor da caixa ligado nele. NÃO configuramos, NÃO lemos.
// A emenda continua feita, mas mexer no interruptor não faz mais nada — ele
// conflitava com o comando do celular e derrubava a placa.

#define TFT_CS -1
#define TFT_DC 2
#define TFT_RST 4

Adafruit_ST7789 tela = Adafruit_ST7789(TFT_CS, TFT_DC, TFT_RST);
WebSocketsClient webSocket;

// ─────────────────────────── estado ───────────────────────────

bool valvulaAberta = false;
unsigned long fecharEm = 0; // millis() em que fecha sozinha; 0 = sem prazo
bool conectadoNaAPI = false;

// O callback do WebSocket só ANOTA o que chegou. Quem age é o loop().
// Desenhar na tela dentro do callback (uns 100ms de SPI) travava o
// WebSocket e estourava o watchdog — era essa a causa dos resets.
volatile bool temComando = false;
volatile bool cmdAbrir = false;
volatile long cmdSegundos = 0;

// Controle de redesenho: só mexe na tela quando algo mudou de verdade.
int ultimoDesenho = -1;
long ultimoMinutoMostrado = -1;

// A válvula é uma bobina: ao ligar e desligar ela solta picos de tensão que
// podem embaralhar o controlador da tela (os pixels coloridos). O ESP32 não
// tem como perceber isso, então a tela é reiniciada logo depois de cada troca
// da válvula — e a cada 10 minutos, por garantia. Leva uns 300 ms.
const unsigned long REINICIAR_TELA_A_CADA_MS = 10UL * 60UL * 1000UL;
unsigned long telaReiniciarEm = 0;   // 0 = nada agendado
unsigned long ultimoReinicioTela = 0;

// ─────────────────────────── válvula ───────────────────────────

// O módulo de relé é acionado em nível BAIXO. Pra fechar deixamos o pino
// como entrada (flutuando) em vez de mandar HIGH — é assim que esta placa
// funciona de verdade, e evita acionar o relé durante o boot.
void abrirValvula(long segundos) {
  pinMode(PINO_RELE, OUTPUT);
  digitalWrite(PINO_RELE, LOW);
  valvulaAberta = true;

  unsigned long duracao =
      (segundos > 0) ? (unsigned long)segundos * 1000UL : MAX_ABERTA_MS;
  if (duracao > MAX_ABERTA_MS)
    duracao = MAX_ABERTA_MS;
  fecharEm = millis() + duracao;

  Serial.printf("ABERTA por %lu s\n", duracao / 1000);
  confirmar();
  telaReiniciarEm = millis() + 300;  // espera o pico da bobina passar
}

void fecharValvula() {
  pinMode(PINO_RELE, INPUT);
  valvulaAberta = false;
  fecharEm = 0;
  Serial.println("FECHADA");
  confirmar();
  telaReiniciarEm = millis() + 300;  // o pico maior é justamente no desligar
}

void confirmar() {
  if (!conectadoNaAPI)
    return;
  webSocket.sendTXT(valvulaAberta ? "{\"aberta\":true}" : "{\"aberta\":false}");
}

// ─────────────────────────── tela ───────────────────────────

// Reset físico (pino RST) + sequência de inicialização completa. Depois disto
// a tela está como recém-ligada, e o próximo atualizarTela() redesenha tudo.
void iniciarTela() {
  tela.init(240, 240, SPI_MODE3);
  tela.setSPISpeed(20000000);
  tela.setRotation(0);
  ultimoDesenho = -1;
  ultimoReinicioTela = millis();
}

void desenharTela() {
  tela.fillScreen(ST77XX_BLACK);

  tela.setTextColor(ST77XX_WHITE);
  tela.setTextSize(3);
  tela.setCursor(10, 12);
  tela.println("IRRIGACAO");

  tela.setTextSize(2);
  tela.setCursor(10, 52);
  tela.print("WiFi ");
  if (WiFi.status() == WL_CONNECTED) {
    tela.setTextColor(ST77XX_GREEN);
    tela.println("ok");
  } else {
    tela.setTextColor(ST77XX_RED);
    tela.println("sem sinal");
  }

  tela.setTextColor(ST77XX_WHITE);
  tela.setCursor(10, 76);
  tela.print("Servidor ");
  if (conectadoNaAPI) {
    tela.setTextColor(ST77XX_GREEN);
    tela.println("ok");
  } else {
    tela.setTextColor(ST77XX_RED);
    tela.println("offline");
  }

  tela.fillRect(10, 112, 220, 84, valvulaAberta ? ST77XX_GREEN : ST77XX_RED);
  tela.setTextColor(ST77XX_BLACK);
  tela.setTextSize(3);
  tela.setCursor(35, 142);
  tela.println(valvulaAberta ? "ABERTA" : "FECHADA");

  ultimoMinutoMostrado = -1;
  desenharRestante();
}

// Mostra os minutos que faltam, redesenhando só essa faixa.
void desenharRestante() {
  long minutos = -1;
  if (valvulaAberta && fecharEm > millis()) {
    minutos = (long)((fecharEm - millis()) / 60000UL) + 1;
  }
  if (minutos == ultimoMinutoMostrado)
    return;
  ultimoMinutoMostrado = minutos;

  tela.fillRect(0, 205, 240, 30, ST77XX_BLACK);
  if (minutos < 0)
    return;

  tela.setTextColor(ST77XX_WHITE);
  tela.setTextSize(2);
  tela.setCursor(10, 210);
  tela.printf("fecha em %ld min", minutos);
}

void atualizarTela() {
  int agora = (WiFi.status() == WL_CONNECTED ? 4 : 0) |
              (conectadoNaAPI ? 2 : 0) | (valvulaAberta ? 1 : 0);
  if (agora != ultimoDesenho) {
    ultimoDesenho = agora;
    desenharTela();
  } else {
    desenharRestante();
  }
}

// ─────────────────────────── WebSocket ───────────────────────────

// O payload NÃO vem garantidamente terminado em zero. Copiar com String()
// direto lia memória além do buffer — outra fonte de travamento.
String textoDe(uint8_t *payload, size_t tamanho) {
  String s;
  s.reserve(tamanho + 1);
  for (size_t i = 0; i < tamanho; i++)
    s += (char)payload[i];
  return s;
}

void aoEventoWebSocket(WStype_t tipo, uint8_t *payload, size_t tamanho) {
  switch (tipo) {
  case WStype_CONNECTED:
    conectadoNaAPI = true;
    Serial.println("conectado na API");
    break;

  case WStype_DISCONNECTED:
    conectadoNaAPI = false;
    Serial.println("caiu a conexao com a API");
    // Perdeu contato com a válvula aberta? Fecha. Ficar aberta sem
    // ninguém pra mandar fechar é alagamento.
    if (valvulaAberta) {
      temComando = true;
      cmdAbrir = false;
      cmdSegundos = 0;
    }
    break;

  case WStype_TEXT: {
    // A mensagem é minúscula e de formato fixo ({"abrir":true,"segundos":600}),
    // então lemos na unha em vez de instalar uma biblioteca de JSON.
    String msg = textoDe(payload, tamanho);
    bool abrir = msg.indexOf("\"abrir\":true") >= 0;
    long segundos = 0;
    int i = msg.indexOf("\"segundos\":");
    if (i >= 0)
      segundos = msg.substring(i + 11).toInt();

    cmdAbrir = abrir;
    cmdSegundos = segundos;
    temComando = true; // o loop() aplica
    break;
  }

  default:
    break;
  }
}

// ─────────────────────────── setup / loop ───────────────────────────

void setup() {
  Serial.begin(115200);
  delay(300);

  // Primeiro de tudo: válvula fechada. Vale pra ligar e pra qualquer reset.
  pinMode(PINO_RELE, INPUT);
  valvulaAberta = false;

  iniciarTela();
  desenharTela();

  WiFi.mode(WIFI_STA);
  WiFi.setAutoReconnect(true);
  WiFi.begin(WIFI_SSID, WIFI_SENHA);

  Serial.print("conectando no wifi");
  unsigned long inicio = millis();
  while (WiFi.status() != WL_CONNECTED && millis() - inicio < 20000) {
    delay(500);
    Serial.print(".");
  }
  Serial.println();

  if (WiFi.status() == WL_CONNECTED) {
    Serial.print("wifi ok, ip ");
    Serial.println(WiFi.localIP());
  } else {
    Serial.println("sem wifi — vai continuar tentando sozinho");
  }

  String cabecalho = String("X-Device-Token: ") + TOKEN;
  webSocket.setExtraHeaders(cabecalho.c_str());

  if (USAR_TLS) {
    webSocket.beginSSL(SERVIDOR, PORTA, CAMINHO);
  } else {
    webSocket.begin(SERVIDOR, PORTA, CAMINHO);
  }

  webSocket.onEvent(aoEventoWebSocket);
  webSocket.setReconnectInterval(5000);
  // ping a cada 15s, espera 3s pelo pong, desiste depois de 2 falhas.
  // É este batimento que detecta queda de internet em segundos.
  webSocket.enableHeartbeat(15000, 3000, 2);

  atualizarTela();
}

void loop() {
  webSocket.loop();

  // 1. chegou comando? aplica aqui, fora do callback.
  if (temComando) {
    temComando = false;
    if (cmdAbrir)
      abrirValvula(cmdSegundos);
    else
      fecharValvula();
  }

  // 2. estourou o prazo? fecha. Esta é a rede de segurança que funciona
  //    mesmo se a API sumir no meio da rega.
  if (valvulaAberta && fecharEm != 0 && (long)(millis() - fecharEm) >= 0) {
    Serial.println("prazo acabou");
    fecharValvula();
  }

  // 3. tela embaralhada? reinicia (depois de mexer na válvula, ou a cada 10 min)
  bool depoisDaValvula = telaReiniciarEm != 0 && (long)(millis() - telaReiniciarEm) >= 0;
  if (depoisDaValvula || millis() - ultimoReinicioTela > REINICIAR_TELA_A_CADA_MS) {
    telaReiniciarEm = 0;
    iniciarTela();
  }

  // 4. tela, no máximo 2x por segundo
  static unsigned long ultimaTela = 0;
  if (millis() - ultimaTela > 500) {
    ultimaTela = millis();
    atualizarTela();
  }

  delay(10);
}
