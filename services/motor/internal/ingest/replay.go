package ingest

// Reproduccion de una carrera ya guardada en MongoDB (HU-04).
//
// La carrera es PASADA: OpenF1 se descarga una vez y aqui se "vuelve a vivir"
// emitiendo sus eventos por WebSocket con los intervalos originales. Para que
// la liquidacion (HU-07) pueda resolver mercados sobre esa carrera, la
// reproduccion deja dos rastros en MongoDB, medidos con el reloj REAL del
// servidor (el mismo que decide cierra_en y observa_hasta):
//
//  1. emitido_en en cada evento de eventos_carrera, al emitirlo. El mercado
//     Si/No ("¿habra algun cambio de posicion en la ventana?") gana "Sí" si
//     hay un evento de posicion con emitido_en dentro de la ventana. El
//     timestamp del evento es historico y no sirve para eso.
//  2. Un latido en la coleccion reproducciones (un documento por carrera)
//     cada IntervaloLatido mientras la reproduccion corre. Sin eventos, gana
//     "No" solo si el latido demuestra que la reproduccion estuvo viva
//     durante toda la ventana; si nadie la lanzo (o se cayo), el mercado
//     queda pendiente en vez de dar "No" por error.
//
// Ademas, la reproduccion puede empezar a mitad de carrera (offset), para no
// arrancar con la parrilla de salida: en la primera vuelta casi todo son
// cambios de posicion y el mercado no tendria gracia. Ver OffsetAleatorio.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	// OffsetMinimo: la reproduccion aleatoria nunca empieza antes de este
	// tiempo de carrera (contado desde la primera posicion). 10 minutos son
	// unas 6 vueltas de ~90 s: ya paso la salida y la primera vuelta.
	OffsetMinimo = 10 * time.Minute
	// MargenFinal: tampoco empieza a menos de esto del final, para que quede
	// carrera para abrir varios mercados (30 s de apuestas + 60 s de
	// observacion cada uno).
	MargenFinal = 15 * time.Minute
	// IntervaloLatido: cada cuanto la reproduccion avisa que sigue viva. La
	// liquidacion de un mercado sin eventos puede esperar hasta este tiempo
	// despues de observa_hasta, hasta que llega un latido posterior.
	IntervaloLatido = 5 * time.Second
)

// ErrSinPosiciones: la carrera no tiene eventos de posicion para reproducir.
var ErrSinPosiciones = errors.New("la carrera no tiene eventos de posicion guardados")

// RangoCarrera devuelve el timestamp (historico) del primer y del ultimo
// evento de posicion de la carrera. Con eso se calcula el offset.
func (s *Servicio) RangoCarrera(ctx context.Context, carreraID string) (inicio, fin time.Time, err error) {
	if inicio, err = s.extremoPosiciones(ctx, carreraID, 1); err != nil {
		return
	}
	fin, err = s.extremoPosiciones(ctx, carreraID, -1)
	return
}

// extremoPosiciones busca el timestamp minimo (orden 1) o maximo (orden -1).
func (s *Servicio) extremoPosiciones(ctx context.Context, carreraID string, orden int) (time.Time, error) {
	var doc struct {
		Timestamp time.Time `bson:"timestamp"`
	}
	opts := options.FindOne().
		SetSort(bson.D{{Key: "timestamp", Value: orden}}).
		SetProjection(bson.M{"timestamp": 1})
	err := s.eventos.FindOne(ctx, bson.M{"carrera_id": carreraID, "tipo": "posicion"}, opts).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, ErrSinPosiciones
	}
	return doc.Timestamp.UTC(), err
}

// OffsetAleatorio elige desde que punto de la carrera reproducir: un valor al
// azar en [OffsetMinimo, duracion-MargenFinal). Si la carrera es demasiado
// corta para eso (p. ej. los pocos eventos del seed), devuelve 0: se
// reproduce desde el principio. azar(n) debe devolver un entero en [0, n);
// en produccion es rand.Int64N y en las pruebas una funcion fija.
func OffsetAleatorio(duracion time.Duration, azar func(n int64) int64) time.Duration {
	hasta := duracion - MargenFinal
	if hasta <= OffsetMinimo {
		return 0
	}
	return OffsetMinimo + time.Duration(azar(int64(hasta-OffsetMinimo)))
}

// Replay lee los eventos de una carrera desde Mongo y los emite por `out`
// respetando los intervalos originales, divididos por `factor`.
// factor=1 → tiempo real; factor=10 → 10x más rápido.
//
// desde (instante historico de la carrera) permite empezar a mitad de
// carrera; con el valor cero se reproduce desde el principio.
//
// Ojo con factor: la ventana de observacion de un mercado dura 60 s REALES,
// que con factor=10 son 10 minutos de carrera (casi seguro gana "Sí"). Para
// la demo del mercado Si/No conviene factor=1.
func (s *Servicio) Replay(ctx context.Context, carreraID string, desde time.Time, factor float64, out chan<- EventoCarrera) error {
	if factor <= 0 {
		factor = 1
	}

	filtro := bson.M{
		"carrera_id": carreraID,
		// La lista de pilotos no es un evento de la carrera: su timestamp es
		// la hora de la ingesta (anos despues de la carrera), asi que dejaria
		// la reproduccion "esperando" al final y latiendo sin eventos, lo que
		// haria ganar "No" a mercados abiertos cuando la carrera ya termino.
		"datos.tipo_vuelta": bson.M{"$ne": "lista_pilotos"},
	}
	if !desde.IsZero() {
		filtro["timestamp"] = bson.M{"$gte": desde}
	}

	cur, err := s.eventos.Find(ctx, filtro,
		options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}}))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	detenerLatido := s.iniciarLatido(ctx, carreraID, desde, factor)
	defer detenerLatido()

	var previo time.Time
	for cur.Next(ctx) {
		var ev EventoCarrera
		if err := cur.Decode(&ev); err != nil {
			return err
		}

		if !previo.IsZero() {
			delta := ev.Timestamp.Sub(previo)
			espera := time.Duration(float64(delta) / factor)
			select {
			case <-time.After(espera):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		previo = ev.Timestamp

		// Se marca ANTES de entregarlo para difundir: si MongoDB falla, se
		// corta la reproduccion, porque un evento emitido sin emitido_en
		// haria que la liquidacion no lo viera. Se trunca a milisegundos
		// porque es la precision con la que MongoDB guarda las fechas.
		ev.EmitidoEn = time.Now().UTC().Truncate(time.Millisecond)
		if _, err := s.eventos.UpdateOne(ctx,
			bson.M{"_id": ev.ID},
			bson.M{"$set": bson.M{"emitido_en": ev.EmitidoEn}},
		); err != nil {
			return fmt.Errorf("marcar emitido_en: %w", err)
		}

		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return cur.Err()
}

// iniciarLatido registra la reproduccion en la coleccion reproducciones
// (_id = carrera_id) y la mantiene "viva" con un latido cada IntervaloLatido.
// La funcion devuelta detiene el latido y marca terminada_en.
//
// Cada reproduccion nueva de la misma carrera reemplaza el documento con su
// propio iniciada_en; las escrituras filtran por ese valor, asi que una
// reproduccion vieja que siga corriendo no pisa a la nueva. La liquidacion
// exige iniciada_en <= cierra_en y latido_en >= observa_hasta: si la
// reproduccion se reinicio o se cayo durante la ventana, no hay prueba de
// "No" y el mercado queda pendiente.
func (s *Servicio) iniciarLatido(ctx context.Context, carreraID string, desde time.Time, factor float64) (detener func()) {
	inicio := time.Now().UTC().Truncate(time.Millisecond)
	set := bson.M{"iniciada_en": inicio, "latido_en": inicio, "terminada_en": nil, "factor": factor}
	actualizacion := bson.M{"$set": set}
	if desde.IsZero() {
		actualizacion["$unset"] = bson.M{"desde": ""}
	} else {
		set["desde"] = desde
	}
	if _, err := s.reproducciones.UpdateOne(ctx, bson.M{"_id": carreraID}, actualizacion,
		options.Update().SetUpsert(true)); err != nil {
		log.Printf("replay %s: no se pudo registrar la reproduccion: %v", carreraID, err)
	}

	esta := bson.M{"_id": carreraID, "iniciada_en": inicio}
	latir := func(ctx context.Context, extra bson.M) {
		set := bson.M{"latido_en": time.Now().UTC().Truncate(time.Millisecond)}
		for k, v := range extra {
			set[k] = v
		}
		if _, err := s.reproducciones.UpdateOne(ctx, esta, bson.M{"$set": set}); err != nil {
			log.Printf("replay %s: no se pudo registrar el latido: %v", carreraID, err)
		}
	}

	parar := make(chan struct{})
	listo := make(chan struct{})
	go func() {
		defer close(listo)
		t := time.NewTicker(IntervaloLatido)
		defer t.Stop()
		for {
			select {
			case <-parar:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				latir(ctx, nil)
			}
		}
	}()

	return func() {
		close(parar)
		<-listo
		// ctx puede estar cancelado (timeout o apagado): se usa uno nuevo
		// para dejar constancia de hasta cuando estuvo viva.
		ctxFin, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		latir(ctxFin, bson.M{"terminada_en": time.Now().UTC().Truncate(time.Millisecond)})
	}
}
