package fichas

import (
	"cmp"
	"slices"
	"time"
)

// FilaRanking es un inscrito en la tabla del torneo (RF-18). Lleva su puesto,
// sus fichas, cuanto ha apostado y el avance hacia el minimo del 50 % que lo
// hace elegible para el reparto del botin (HU2-12).
type FilaRanking struct {
	Puesto    int     `json:"puesto"`
	UsuarioID string  `json:"usuario_id"`
	Fichas    int64   `json:"fichas"`
	Apostado  int64   `json:"apostado"`
	AvancePct float64 `json:"avance_pct"` // 0..100, apostado frente al minimo
	Elegible  bool    `json:"elegible"`   // ya aposto el minimo para el reparto
}

// Ranking es la foto completa del torneo en un instante. Es el payload del
// mensaje WebSocket "ranking_torneo" (RF-18). Sigue el mismo patron que
// FotoPosiciones de HU-05: lleva actualizado_en/emitido_en para que el front
// descarte fotos viejas y se pueda medir la latencia (RNF-13).
type Ranking struct {
	EventoID      string        `json:"evento_id"`
	Filas         []FilaRanking `json:"filas"`
	MinApostado   int64         `json:"min_apostado"` // fichas que hay que apostar para ser elegible
	Botin         int64         `json:"botin"`        // pot del evento (informativo)
	ActualizadoEn time.Time     `json:"actualizado_en"`
	EmitidoEn     time.Time     `json:"emitido_en,omitzero"`
}

// minApostadoPara calcula el umbral de fichas apostadas (el 50 % de las
// fichas iniciales por defecto) que hace a un jugador elegible para el
// reparto. Se redondea hacia arriba para no dejar fuera a quien aposto justo
// la mitad por un decimal.
func minApostadoPara(fichasIniciales int64, pct float64) int64 {
	if fichasIniciales <= 0 || pct <= 0 {
		return 0
	}
	umbral := float64(fichasIniciales) * pct / 100.0
	entero := int64(umbral)
	if float64(entero) < umbral {
		entero++ // techo: si no es exacto, exige una ficha mas
	}
	return entero
}

// ConstruirRanking ordena a los inscritos por fichas (de mayor a menor) y
// calcula el puesto, el avance y la elegibilidad de cada uno. Es una funcion
// pura: no toca Redis ni el reloj, para poder probarla directamente.
//
// El puesto refleja empates con la regla del enunciado: cada grupo empatado
// ocupa un solo puesto y el siguiente grupo toma el puesto que sigue (puesto
// denso: 1,1,1,2). "Si tres empatan en el 1.º ... el siguiente jugador es el
// 2.º". Es la misma regla con la que HU2-12 reparte el botin.
// El desempate visual es por usuario_id, solo para que el orden sea estable.
func ConstruirRanking(eventoID string, estados []Estado, fichasIniciales int64, minWagerPct float64, botin int64, ahora time.Time) Ranking {
	minApostado := minApostadoPara(fichasIniciales, minWagerPct)

	filas := make([]FilaRanking, 0, len(estados))
	for _, e := range estados {
		fila := FilaRanking{
			UsuarioID: e.UsuarioID,
			Fichas:    e.Fichas,
			Apostado:  e.Apostado,
			// Misma regla que el reparto de Pagos (HU2-12): con minimo 0
			// (EVENT_MIN_WAGER_PCT=0) todos son elegibles.
			Elegible: e.Apostado >= minApostado,
		}
		fila.AvancePct = avance(e.Apostado, minApostado)
		filas = append(filas, fila)
	}

	// Orden: mas fichas primero; a igualdad, por usuario para que sea estable.
	slices.SortFunc(filas, func(a, b FilaRanking) int {
		return cmp.Or(
			cmp.Compare(b.Fichas, a.Fichas),
			cmp.Compare(a.UsuarioID, b.UsuarioID),
		)
	})

	// Puesto denso: mismo numero de fichas => mismo puesto; el siguiente grupo
	// toma el puesto inmediato (1,1,1,2), como el reparto de HU2-12.
	for i := range filas {
		switch {
		case i == 0:
			filas[i].Puesto = 1
		case filas[i].Fichas == filas[i-1].Fichas:
			filas[i].Puesto = filas[i-1].Puesto
		default:
			filas[i].Puesto = filas[i-1].Puesto + 1
		}
	}

	return Ranking{
		EventoID:      eventoID,
		Filas:         filas,
		MinApostado:   minApostado,
		Botin:         botin,
		ActualizadoEn: ahora.UTC(),
	}
}

// avance expresa el apostado como porcentaje del minimo (0..100). Al llegar o
// superar el minimo se topa en 100: la barra del front no pasa de ahi.
func avance(apostado, minApostado int64) float64 {
	if minApostado <= 0 || apostado >= minApostado {
		// Sin minimo (0 %) o ya alcanzado: la barra esta completa.
		return 100
	}
	return float64(apostado) / float64(minApostado) * 100.0
}
