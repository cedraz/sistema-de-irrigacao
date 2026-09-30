package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/cedraz/sistema-de-irrigacao/api/internal/store"
)

func (s *Server) Rotas() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/estado", s.handleEstado)
	mux.HandleFunc("POST /api/valvula", s.handleValvula)

	mux.HandleFunc("GET /api/programacoes", s.handleListarProgramacoes)
	mux.HandleFunc("POST /api/programacoes", s.handleCriarProgramacao)
	mux.HandleFunc("PUT /api/programacoes/{id}", s.handleAtualizarProgramacao)
	mux.HandleFunc("DELETE /api/programacoes/{id}", s.handleRemoverProgramacao)

	mux.HandleFunc("GET /api/historico", s.handleHistorico)

	mux.HandleFunc("GET /ws/dispositivo", s.handleDispositivoWS)

	return mux
}

// ---------- estado e controle manual ----------

type estadoResp struct {
	Aberta    bool       `json:"aberta"`
	Confirmou bool       `json:"confirmou"` // o que o ESP32 respondeu de verdade
	Online    bool       `json:"online"`
	Desde     *time.Time `json:"desde"`
	FechaEm   *time.Time `json:"fecha_em"`
	Proxima   *time.Time `json:"proxima"`
}

func (s *Server) handleEstado(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := estadoResp{Aberta: s.aberta}
	if s.aberta {
		desde, fecha := s.desde, s.fechaEm
		out.Desde, out.FechaEm = &desde, &fecha
	}
	s.mu.Unlock()

	out.Confirmou, out.Online = s.dev.estado()
	out.Proxima = s.Proxima(r.Context())

	escreverJSON(w, http.StatusOK, out)
}

func (s *Server) handleValvula(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Abrir   bool `json:"abrir"`
		Minutos int  `json:"minutos"`
	}
	if err := lerJSON(r, &in); err != nil {
		escreverErro(w, http.StatusBadRequest, "json invalido")
		return
	}

	var err error
	if in.Abrir {
		err = s.Abrir(r.Context(), "manual", in.Minutos)
	} else {
		err = s.Fechar(r.Context())
	}
	if err != nil {
		log.Printf("valvula: %v", err)
		escreverErro(w, http.StatusInternalServerError, "nao deu pra mandar o comando")
		return
	}

	s.handleEstado(w, r)
}

// ---------- programações ----------

func (s *Server) handleListarProgramacoes(w http.ResponseWriter, r *http.Request) {
	lista, err := s.store.ListarProgramacoes(r.Context())
	if err != nil {
		log.Printf("listar programacoes: %v", err)
		escreverErro(w, http.StatusInternalServerError, "nao deu pra ler os horarios")
		return
	}
	escreverJSON(w, http.StatusOK, lista)
}

func (s *Server) handleCriarProgramacao(w http.ResponseWriter, r *http.Request) {
	p, ok := lerProgramacao(w, r)
	if !ok {
		return
	}
	criada, err := s.store.CriarProgramacao(r.Context(), p)
	if err != nil {
		log.Printf("criar programacao: %v", err)
		escreverErro(w, http.StatusInternalServerError, "nao deu pra salvar")
		return
	}
	escreverJSON(w, http.StatusCreated, criada)
}

func (s *Server) handleAtualizarProgramacao(w http.ResponseWriter, r *http.Request) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		escreverErro(w, http.StatusBadRequest, "id invalido")
		return
	}
	p, ok := lerProgramacao(w, r)
	if !ok {
		return
	}
	if err := s.store.AtualizarProgramacao(r.Context(), id, p); err != nil {
		log.Printf("atualizar programacao: %v", err)
		escreverErro(w, http.StatusInternalServerError, "nao deu pra salvar")
		return
	}
	escreverJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRemoverProgramacao(w http.ResponseWriter, r *http.Request) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		escreverErro(w, http.StatusBadRequest, "id invalido")
		return
	}
	if err := s.store.RemoverProgramacao(r.Context(), id); err != nil {
		log.Printf("remover programacao: %v", err)
		escreverErro(w, http.StatusInternalServerError, "nao deu pra apagar")
		return
	}
	escreverJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// lerProgramacao decodifica e valida. Validação no servidor porque o site é
// html puro e qualquer um pode mandar o que quiser direto na API.
func lerProgramacao(w http.ResponseWriter, r *http.Request) (store.Programacao, bool) {
	var p store.Programacao
	if err := lerJSON(r, &p); err != nil {
		escreverErro(w, http.StatusBadRequest, "json invalido")
		return p, false
	}

	if _, err := time.Parse("15:04", p.Hora); err != nil {
		escreverErro(w, http.StatusBadRequest, "hora precisa ser no formato 07:00")
		return p, false
	}
	if p.Duracao < 1 || p.Duracao > maxMinutos {
		escreverErro(w, http.StatusBadRequest, "duracao precisa ser de 1 a 120 minutos")
		return p, false
	}
	if len(p.Dias) == 0 {
		escreverErro(w, http.StatusBadRequest, "escolha pelo menos um dia")
		return p, false
	}
	for _, d := range p.Dias {
		if d < 0 || d > 6 {
			escreverErro(w, http.StatusBadRequest, "dia invalido")
			return p, false
		}
	}
	return p, true
}

// ---------- histórico ----------

func (s *Server) handleHistorico(w http.ResponseWriter, r *http.Request) {
	limite, _ := strconv.ParseInt(r.URL.Query().Get("limite"), 10, 64)
	if limite <= 0 || limite > 100 {
		limite = 20
	}
	lista, err := s.store.Historico(r.Context(), limite)
	if err != nil {
		log.Printf("historico: %v", err)
		escreverErro(w, http.StatusInternalServerError, "nao deu pra ler o historico")
		return
	}
	escreverJSON(w, http.StatusOK, lista)
}

// ---------- ajudantes ----------

func escreverJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("escrever resposta: %v", err)
	}
}

func escreverErro(w http.ResponseWriter, status int, msg string) {
	escreverJSON(w, status, map[string]string{"error": msg})
}

// lerJSON limita o corpo a 1MB: sem isso alguém manda um JSON gigante e
// derruba a API por falta de memória.
func lerJSON(r *http.Request, dst any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
}
