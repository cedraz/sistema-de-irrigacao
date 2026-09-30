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

// comando é o que a API manda pro ESP32.
type comando struct {
	Abrir    bool `json:"abrir"`
	Segundos int  `json:"segundos"` // quanto tempo ficar aberta; 0 quando fecha
}

// resposta é o que o ESP32 devolve confirmando o que ele fez de verdade.
type resposta struct {
	Aberta bool `json:"aberta"`
}

// dispositivo guarda A conexão do ESP32.
// Um aparelho só, então um ponteiro com mutex resolve — hub de conexões
// quando existir uma segunda válvula, não antes.
type dispositivo struct {
	mu        sync.Mutex
	conn      *websocket.Conn
	confirmou bool      // último estado que o ESP32 confirmou
	visto     time.Time // quando ele confirmou pela última vez
}

func (d *dispositivo) online() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn != nil
}

func (d *dispositivo) estado() (confirmou bool, online bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.confirmou, d.conn != nil
}

// enviar manda o comando. Silencioso se o ESP32 estiver offline: quando ele
// reconectar recebe o estado atual no handshake.
func (d *dispositivo) enviar(ctx context.Context, abrir bool, segundos int) {
	// O mutex também serializa as escritas: dois comandos ao mesmo tempo
	// escrevendo no mesmo conn dá pânico.
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	b, err := json.Marshal(comando{Abrir: abrir, Segundos: segundos})
	if err != nil {
		return
	}
	if err := d.conn.Write(ctx, websocket.MessageText, b); err != nil {
		log.Printf("falhou mandar comando pro esp32: %v", err)
	}
}

// trocar põe a conexão nova no lugar e fecha a antiga. O ESP32 reinicia e
// reconecta antes do TCP velho morrer; sem isto sobra conexão zumbi.
func (d *dispositivo) trocar(c *websocket.Conn) {
	d.mu.Lock()
	antiga := d.conn
	d.conn = c
	d.mu.Unlock()

	if antiga != nil {
		antiga.Close(websocket.StatusNormalClosure, "outra conexao assumiu")
	}
}

func (d *dispositivo) remover(c *websocket.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == c {
		d.conn = nil
	}
}

func (s *Server) handleDispositivoWS(w http.ResponseWriter, r *http.Request) {
	// ConstantTimeCompare em vez de ==: comparação que sai cedo vaza o token
	// pelo tempo de resposta.
	enviado := []byte(r.Header.Get("X-Device-Token"))
	esperado := []byte(s.token)
	if len(esperado) == 0 || subtle.ConstantTimeCompare(enviado, esperado) != 1 {
		http.Error(w, "token invalido", http.StatusUnauthorized)
		return
	}

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("accept ws: %v", err)
		return
	}
	defer c.CloseNow()

	s.dev.trocar(c)
	defer s.dev.remover(c)

	log.Println("esp32 conectou")
	defer log.Println("esp32 desconectou")

	ctx, cancelar := context.WithCancel(r.Context())
	defer cancelar()

	// Ping do lado da API. Se o ESP32 perde a energia no meio, a conexão não
	// fecha — ela só fica muda. Sem isto a API continua achando que ele está
	// online por muito tempo, e o site mostra "aparelho ok" com ele morto.
	go func() {
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
					log.Printf("esp32 nao respondeu ao ping: %v", err)
					c.Close(websocket.StatusGoingAway, "sem resposta")
					return
				}
			}
		}
	}()

	// Ressincroniza na hora: o ESP32 pode ter perdido um comando enquanto
	// estava fora. Sem isto ele volta achando que está fechado enquanto a API
	// acha que está aberta.
	aberta, segundos := s.estadoDesejado()
	s.dev.enviar(ctx, aberta, segundos)

	// Lê as confirmações do ESP32. Este Read também é quem recebe os pongs.
	for {
		_, dados, err := c.Read(ctx)
		if err != nil {
			return
		}
		var resp resposta
		if json.Unmarshal(dados, &resp) != nil {
			continue
		}
		s.dev.mu.Lock()
		s.dev.confirmou = resp.Aberta
		s.dev.visto = time.Now()
		s.dev.mu.Unlock()

		if resp.Aberta {
			if err := s.store.ConfirmarRegaAberta(ctx); err != nil {
				log.Printf("confirmar rega: %v", err)
			}
		}
	}
}
