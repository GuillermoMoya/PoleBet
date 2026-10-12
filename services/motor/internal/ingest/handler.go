package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/arquisoft/motor/internal/auth"
	"github.com/arquisoft/motor/internal/mongodb"
	"github.com/arquisoft/motor/internal/ws"
)

// Register monta las rutas de ingesta (HU-04).
//
//	POST /ingest/openf1  → trae datos reales de OpenF1 y los guarda en Mongo
//	POST /ingest/replay  → reproduce por WebSocket una carrera ya guardada
//
// Ambas rutas exigen rol administrador.
func Register(
	mux *http.ServeMux,
	db *mongodb.Conexion,
	hub *ws.Hub,
	a *auth.Autenticador,
) {
	svc := NuevoServicio(db.DB)

	soloAdmin := func(h http.Handler) http.Handler {
		return a.ExigirRol(auth.RolAdmin, h)
	}

	// ---- POST /ingest/openf1 ---------------------------------------------
	mux.Handle("POST /ingest/openf1", soloAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CarreraID  string `json:"carrera_id"`
			SessionKey int    `json:"session_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "json invalido: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.CarreraID == "" || body.SessionKey == 0 {
			http.Error(w, "carrera_id y session_key son obligatorios", http.StatusBadRequest)
			return
		}

		n, err := svc.IngestaOpenF1(r.Context(), body.CarreraID, body.SessionKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"insertados": n})
	})))

	// ---- POST /ingest/replay ---------------------------------------------
	//
	// Cuerpo: {"carrera_id": "...", "factor": 1, "offset_segundos": 1200}
	//   - factor: velocidad (1 = tiempo real; es el recomendado para los
	//     mercados Si/No, ver Replay).
	//   - offset_segundos (opcional): desde que segundo de la carrera
	//     reproducir, contado desde la primera posicion. Si no viene, se elige
	//     al azar despues de las primeras vueltas (OffsetAleatorio), para no
	//     empezar con la parrilla de salida. Sirve fijarlo para repetir una
	//     prueba.
	mux.Handle("POST /ingest/replay", soloAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CarreraID      string   `json:"carrera_id"`
			Factor         float64  `json:"factor"`
			OffsetSegundos *float64 `json:"offset_segundos"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "json invalido: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.CarreraID == "" {
			http.Error(w, "carrera_id es obligatorio", http.StatusBadRequest)
			return
		}
		if body.Factor <= 0 {
			body.Factor = 1
		}

		inicio, fin, err := svc.RangoCarrera(r.Context(), body.CarreraID)
		if errors.Is(err, ErrSinPosiciones) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "no se pudo leer la carrera: "+err.Error(), http.StatusInternalServerError)
			return
		}
		duracion := fin.Sub(inicio)
		var offset time.Duration
		if body.OffsetSegundos != nil {
			offset = time.Duration(*body.OffsetSegundos * float64(time.Second))
			if offset < 0 || offset > duracion {
				http.Error(w, fmt.Sprintf("offset_segundos debe estar entre 0 y %.0f", duracion.Seconds()), http.StatusBadRequest)
				return
			}
		} else {
			offset = OffsetAleatorio(duracion, rand.Int64N)
		}
		desde := inicio.Add(offset)
		log.Printf("replay %s: desde %s (offset %s de %s), factor %g",
			body.CarreraID, desde.Format(time.RFC3339), offset.Round(time.Second), duracion.Round(time.Second), body.Factor)

		// La reproducción corre en background y difunde cada evento.
		go func(carreraID string, factor float64) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			eventos := make(chan EventoCarrera, 64)
			go func() {
				defer close(eventos)
				if err := svc.Replay(ctx, carreraID, desde, factor, eventos); err != nil {
					log.Printf("replay %s termino con error: %v", carreraID, err)
				}
			}()

			for ev := range eventos {
				if err := hub.Difundir(ws.Mensaje{
					Type:    ws.TipoEventoCarrera,
					Payload: ev,
				}); err != nil {
					log.Printf("no se pudo difundir evento: %v", err)
					return
				}
			}
			log.Printf("replay %s terminado", carreraID)
		}(body.CarreraID, body.Factor)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"estado":          "reproduciendo",
			"carrera_id":      body.CarreraID,
			"factor":          body.Factor,
			"offset_segundos": offset.Seconds(),
			"desde":           desde, // instante (historico) de la carrera
		})
	})))
}
