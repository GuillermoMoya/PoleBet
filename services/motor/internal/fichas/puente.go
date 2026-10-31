package fichas

import "context"

// LiquidacionTorneo es el resultado de liquidar una apuesta de torneo: lo que
// el worker de liquidacion necesita para notificar al jugador (RF-17). Este
// paquete NO importa el paquete liquidacion (evita un ciclo); el adaptador que
// cumple liquidacion.LiquidadorTorneo se arma en main.go a partir de estos
// datos.
type LiquidacionTorneo struct {
	FichasGanadas int64
	FichasTotal   int64
	Puesto        int
}

// LiquidarYUbicar liquida una apuesta de torneo y calcula el puesto del
// jugador en el ranking resultante. Reune en una sola operacion lo que el
// worker necesita (fichas ganadas por la apuesta, saldo total y puesto),
// manteniendo la idempotencia del almacen (repetir no paga doble).
func (a *Almacen) LiquidarYUbicar(ctx context.Context, eventoID, apuestaID string, gano bool, multiplicador float64) (LiquidacionTorneo, error) {
	// Leer la reserva para saber el usuario y las fichas pagadas de ESTA
	// apuesta (el almacen expone el estado del jugador, no el pago puntual).
	reserva, existe, err := a.store.Reserva(ctx, eventoID, apuestaID)
	if err != nil {
		return LiquidacionTorneo{}, err
	}
	if !existe {
		return LiquidacionTorneo{}, ErrJugadorNoInscrito
	}

	// El multiplicador efectivo es el de la reserva; el que llega por
	// parametro (del contexto) se usa solo si la reserva no lo tiene, por
	// compatibilidad. En la practica coinciden.
	mult := reserva.Multiplic
	if mult <= 0 {
		mult = multiplicador
	}
	fichasGanadas := int64(0)
	if gano {
		fichasGanadas = int64(float64(reserva.Monto) * mult)
	}

	estado, err := a.Liquidar(ctx, eventoID, apuestaID, gano)
	if err != nil {
		return LiquidacionTorneo{}, err
	}

	puesto, err := a.puestoDe(ctx, eventoID, reserva.UsuarioID)
	if err != nil {
		return LiquidacionTorneo{}, err
	}
	return LiquidacionTorneo{
		FichasGanadas: fichasGanadas,
		FichasTotal:   estado.Fichas,
		Puesto:        puesto,
	}, nil
}

// puestoDe calcula el puesto actual del jugador en el ranking (con empates).
func (a *Almacen) puestoDe(ctx context.Context, eventoID, usuarioID string) (int, error) {
	estados, err := a.store.Estados(ctx, eventoID)
	if err != nil {
		return 0, err
	}
	// Reusa la logica de puestos del ranking (ConstruirRanking); los
	// parametros de fichas/min/botin no afectan el puesto.
	r := ConstruirRanking(eventoID, estados, 1, 0, 0, a.ahora())
	for _, f := range r.Filas {
		if f.UsuarioID == usuarioID {
			return f.Puesto, nil
		}
	}
	return 0, nil
}
