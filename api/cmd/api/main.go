package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embute o banco de fusos horários no binário. Sem isto,
	// LoadLocation("America/Sao_Paulo") falha na imagem Docker enxuta —
	// e a irrigação regaria no horário errado.
	_ "time/tzdata"

	"github.com/cedraz/sistema-de-irrigacao/api/internal/api"
	"github.com/cedraz/sistema-de-irrigacao/api/internal/store"
)

func env(chave, padrao string) string {
	if v := os.Getenv(chave); v != "" {
		return v
	}
	return padrao
}

func main() {
	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer parar()

	fuso, err := time.LoadLocation(env("TZ", "America/Sao_Paulo"))
	if err != nil {
		log.Fatalf("fuso horario: %v", err)
	}

	st, err := store.New(ctx, env("MONGO_URL", "mongodb://localhost:27017"), env("MONGO_DB", "irrigacao"))
	if err != nil {
		log.Fatalf("mongo: %v", err)
	}
	defer st.Close(context.Background())

	if n, err := st.FecharPendentes(ctx); err != nil {
		log.Printf("fechar regas pendentes: %v", err)
	} else if n > 0 {
		log.Printf("%d rega(s) ficaram abertas quando o servidor caiu; fechei com horario estimado", n)
	}

	token := os.Getenv("DEVICE_TOKEN")
	if token == "" {
		log.Fatal("DEVICE_TOKEN vazio — o ESP32 nao teria como se autenticar")
	}

	srv := api.NewServer(st, token, fuso)
	go srv.Agendador(ctx)

	h := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           srv.Rotas(),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout fica ZERO de propósito: qualquer valor aqui mata a
		// conexão WebSocket do ESP32 no meio.
	}

	go func() {
		log.Printf("ouvindo em %s (fuso %s)", h.Addr, fuso)
		if err := h.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("servidor: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("encerrando...")

	// Fecha a válvula antes de morrer. Se a API cair com a água aberta e o
	// ESP32 continuar online, ninguém mais manda fechar.
	desliga, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelar()
	if err := srv.Fechar(desliga); err != nil {
		log.Printf("fechar na saida: %v", err)
	}
	_ = h.Shutdown(desliga)
}
