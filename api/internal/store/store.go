// Package store guarda tudo que fala com o MongoDB.
// Não sabe o que é HTTP.
package store

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Programacao é uma rega agendada: "às 07:00, por 10 minutos, seg a sex".
type Programacao struct {
	ID      bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Hora    string        `bson:"hora"          json:"hora"`    // "07:00"
	Duracao int           `bson:"duracao"       json:"duracao"` // minutos
	Dias    []int         `bson:"dias"          json:"dias"`    // 0=domingo ... 6=sábado
	Ativo   bool          `bson:"ativo"         json:"ativo"`
}

// Rega é uma abertura da válvula que aconteceu.
type Rega struct {
	ID     bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Inicio time.Time     `bson:"inicio"        json:"inicio"`
	Fim    *time.Time    `bson:"fim,omitempty" json:"fim"`    // nil = ainda aberta
	Origem string        `bson:"origem"        json:"origem"` // "manual" | "agenda"

	// Quando era pra fechar. Serve pra estimar o fim se o servidor cair no meio.
	Previsto *time.Time `bson:"previsto,omitempty" json:"previsto"`

	// O ESP32 respondeu que abriu de verdade? Ponteiro porque as regas gravadas
	// antes deste campo existir não têm a informação — e "não sei" é diferente
	// de "não abriu".
	Confirmada *bool `bson:"confirmada,omitempty" json:"confirmada"`

	// O fim foi deduzido na volta do servidor, não observado na hora.
	Estimado bool `bson:"estimado,omitempty" json:"estimado"`
}

type Store struct {
	programacoes *mongo.Collection
	historico    *mongo.Collection
	client       *mongo.Client
}

func New(ctx context.Context, uri, banco string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}

	db := client.Database(banco)
	return &Store{
		programacoes: db.Collection("programacoes"),
		historico:    db.Collection("historico"),
		client:       client,
	}, nil
}

func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

func (s *Store) Ping(ctx context.Context) error { return s.client.Ping(ctx, nil) }

// ---------- programações ----------

func (s *Store) ListarProgramacoes(ctx context.Context) ([]Programacao, error) {
	cur, err := s.programacoes.Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"hora": 1}))
	if err != nil {
		return nil, err
	}
	// Slice vazio e não nil: nil vira "null" no JSON e quebra o .map() do site.
	lista := []Programacao{}
	if err := cur.All(ctx, &lista); err != nil {
		return nil, err
	}
	return lista, nil
}

func (s *Store) CriarProgramacao(ctx context.Context, p Programacao) (Programacao, error) {
	p.ID = bson.NewObjectID()
	_, err := s.programacoes.InsertOne(ctx, p)
	return p, err
}

func (s *Store) AtualizarProgramacao(ctx context.Context, id bson.ObjectID, p Programacao) error {
	_, err := s.programacoes.UpdateByID(ctx, id, bson.M{"$set": bson.M{
		"hora": p.Hora, "duracao": p.Duracao, "dias": p.Dias, "ativo": p.Ativo,
	}})
	return err
}

func (s *Store) RemoverProgramacao(ctx context.Context, id bson.ObjectID) error {
	_, err := s.programacoes.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

// ---------- histórico ----------

func (s *Store) AbrirRega(ctx context.Context, origem string, previsto time.Time) (Rega, error) {
	nao := false
	r := Rega{
		ID:         bson.NewObjectID(),
		Inicio:     time.Now(),
		Origem:     origem,
		Previsto:   &previsto,
		Confirmada: &nao, // vira true quando o ESP32 responder que abriu
	}
	_, err := s.historico.InsertOne(ctx, r)
	return r, err
}

// FecharRegaAberta preenche o fim da rega que estiver em aberto, se houver.
func (s *Store) FecharRegaAberta(ctx context.Context) error {
	agora := time.Now()
	_, err := s.historico.UpdateMany(ctx,
		bson.M{"fim": bson.M{"$eq": nil}},
		bson.M{"$set": bson.M{"fim": agora}},
	)
	return err
}

// ConfirmarRegaAberta marca que o ESP32 respondeu que abriu de verdade.
func (s *Store) ConfirmarRegaAberta(ctx context.Context) error {
	_, err := s.historico.UpdateMany(ctx,
		bson.M{"fim": nil, "confirmada": false},
		bson.M{"$set": bson.M{"confirmada": true}},
	)
	return err
}

// FecharPendentes fecha regas que ficaram abertas porque o servidor morreu no
// meio (falta de luz, kill -9). Sem isto elas apareceriam "abertas" no
// histórico pra sempre.
//
// O fim usado é o menor entre o horário previsto e agora: o ESP32 fecha
// sozinho quando o prazo acaba ou quando perde a conexão, então a válvula não
// ficou aberta além disso. É estimativa, e fica marcada como tal.
func (s *Store) FecharPendentes(ctx context.Context) (int64, error) {
	agora := time.Now()
	res, err := s.historico.UpdateMany(ctx,
		bson.M{"fim": nil},
		mongo.Pipeline{bson.D{{Key: "$set", Value: bson.D{
			{Key: "fim", Value: bson.D{{Key: "$min", Value: bson.A{"$previsto", agora}}}},
			{Key: "estimado", Value: true},
		}}}},
	)
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

func (s *Store) RegaAberta(ctx context.Context) (*Rega, error) {
	var r Rega
	err := s.historico.FindOne(ctx, bson.M{"fim": bson.M{"$eq": nil}}).Decode(&r)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) Historico(ctx context.Context, limite int64) ([]Rega, error) {
	cur, err := s.historico.Find(ctx, bson.M{},
		options.Find().SetSort(bson.M{"inicio": -1}).SetLimit(limite))
	if err != nil {
		return nil, err
	}
	lista := []Rega{}
	if err := cur.All(ctx, &lista); err != nil {
		return nil, err
	}
	return lista, nil
}
