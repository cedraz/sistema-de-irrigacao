package api

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/cedraz/sistema-de-irrigacao/api/internal/store"
)

// maxMinutos é o teto de qualquer abertura, venha de onde vier.
// Um dedo escorregando e digitando 600 em vez de 60 não vira enchente.
const maxMinutos = 120

type Server struct {
	store *store.Store
	dev   *dispositivo
	token string
	tz    *time.Location

	mu      sync.Mutex
	aberta  bool
	desde   time.Time
	fechaEm time.Time
}

func NewServer(st *store.Store, token string, tz *time.Location) *Server {
	return &Server{
		store: st,
		dev:   &dispositivo{},
		token: token,
		tz:    tz,
	}
}

func (s *Server) estadoDesejado() (aberta bool, segundos int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.aberta {
		return false, 0
	}
	restante := int(time.Until(s.fechaEm).Seconds())
	if restante < 0 {
		restante = 0
	}
	return true, restante
}

// Abrir liga a válvula por N minutos. Chamar de novo com a válvula já aberta
// só estica o prazo — não cria uma segunda rega no histórico.
func (s *Server) Abrir(ctx context.Context, origem string, minutos int) error {
	if minutos <= 0 {
		minutos = 1
	}
	if minutos > maxMinutos {
		minutos = maxMinutos
	}

	s.mu.Lock()
	jaEstava := s.aberta
	s.aberta = true
	if !jaEstava {
		s.desde = time.Now()
	}
	s.fechaEm = time.Now().Add(time.Duration(minutos) * time.Minute)
	previsto := s.fechaEm
	s.mu.Unlock()

	if !jaEstava {
		if _, err := s.store.AbrirRega(ctx, origem, previsto); err != nil {
			return err
		}
	}

	// É esta linha que acende o relé.
	s.dev.enviar(ctx, true, minutos*60)
	log.Printf("valvula ABERTA (%s, %d min)", origem, minutos)
	return nil
}

func (s *Server) Fechar(ctx context.Context) error {
	s.mu.Lock()
	jaEstava := s.aberta
	s.aberta = false
	s.fechaEm = time.Time{}
	s.mu.Unlock()

	if jaEstava {
		if err := s.store.FecharRegaAberta(ctx); err != nil {
			return err
		}
		log.Println("valvula FECHADA")
	}
	s.dev.enviar(ctx, false, 0)
	return nil
}

// Agendador roda pra sempre, com tick de 1 segundo:
//   - o prazo de fechamento é conferido a cada segundo — é isso que faz o
//     histórico registrar "5 min 00 s" e não "5 min 17 s";
//   - a agenda é consultada uma vez por minuto, quando o relógio vira.
//     Checar uma vez só por minuto já impede a mesma programação de disparar
//     duas vezes.
func (s *Server) Agendador(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	ultimoMinuto := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.fecharSeVenceu(ctx)

			minuto := time.Now().In(s.tz).Format("2006-01-02 15:04")
			if minuto != ultimoMinuto && s.verificarAgenda(ctx) {
				ultimoMinuto = minuto // só marca se conseguiu ler: erro no banco tenta de novo no próximo segundo
			}
		}
	}
}

func (s *Server) fecharSeVenceu(ctx context.Context) {
	s.mu.Lock()
	venceu := s.aberta && !s.fechaEm.IsZero() && time.Now().After(s.fechaEm)
	s.mu.Unlock()
	if venceu {
		if err := s.Fechar(ctx); err != nil {
			log.Printf("fechar por tempo: %v", err)
		}
	}
}

// verificarAgenda abre a válvula se alguma programação bate com este minuto.
// Devolve false se não conseguiu ler as programações.
func (s *Server) verificarAgenda(ctx context.Context) bool {
	lista, err := s.store.ListarProgramacoes(ctx)
	if err != nil {
		log.Printf("agendador: %v", err)
		return false
	}

	agora := time.Now().In(s.tz)
	hhmm := agora.Format("15:04")
	for _, p := range lista {
		if !p.Ativo || p.Hora != hhmm || !contem(p.Dias, int(agora.Weekday())) {
			continue
		}
		if err := s.Abrir(ctx, "agenda", p.Duracao); err != nil {
			log.Printf("agendador abrir: %v", err)
		}
	}
	return true
}

// Proxima devolve quando a próxima rega programada acontece, ou nil.
// Olha os próximos 8 dias — cobre qualquer combinação de dias da semana.
func (s *Server) Proxima(ctx context.Context) *time.Time {
	lista, err := s.store.ListarProgramacoes(ctx)
	if err != nil {
		return nil
	}

	agora := time.Now().In(s.tz)
	var candidatas []time.Time

	for _, p := range lista {
		if !p.Ativo {
			continue
		}
		t, err := time.Parse("15:04", p.Hora)
		if err != nil {
			continue
		}
		for d := 0; d < 8; d++ {
			dia := agora.AddDate(0, 0, d)
			if !contem(p.Dias, int(dia.Weekday())) {
				continue
			}
			quando := time.Date(dia.Year(), dia.Month(), dia.Day(),
				t.Hour(), t.Minute(), 0, 0, s.tz)
			if quando.After(agora) {
				candidatas = append(candidatas, quando)
				break
			}
		}
	}

	if len(candidatas) == 0 {
		return nil
	}
	sort.Slice(candidatas, func(i, j int) bool { return candidatas[i].Before(candidatas[j]) })
	return &candidatas[0]
}

func contem(lista []int, n int) bool {
	for _, v := range lista {
		if v == n {
			return true
		}
	}
	return false
}
