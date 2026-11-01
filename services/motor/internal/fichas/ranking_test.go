package fichas

import (
	"testing"
	"time"
)

var instante = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestConstruirRankingOrdenaPorFichas(t *testing.T) {
	estados := []Estado{
		{UsuarioID: "u1", Fichas: 500, Apostado: 600},
		{UsuarioID: "u2", Fichas: 1200, Apostado: 800},
		{UsuarioID: "u3", Fichas: 900, Apostado: 100},
	}
	r := ConstruirRanking("e1", estados, 1000, 50, 5000, instante)
	if len(r.Filas) != 3 {
		t.Fatalf("quiero 3 filas, tengo %d", len(r.Filas))
	}
	if r.Filas[0].UsuarioID != "u2" || r.Filas[0].Puesto != 1 {
		t.Fatalf("P1 deberia ser u2: %+v", r.Filas[0])
	}
	if r.Filas[1].UsuarioID != "u3" || r.Filas[1].Puesto != 2 {
		t.Fatalf("P2 deberia ser u3: %+v", r.Filas[1])
	}
	if r.Filas[2].UsuarioID != "u1" || r.Filas[2].Puesto != 3 {
		t.Fatalf("P3 deberia ser u1: %+v", r.Filas[2])
	}
}

func TestConstruirRankingEmpatesCompartenPuesto(t *testing.T) {
	// Tres empatados en el 1.º: comparten puesto 1 y el siguiente es el 2.º
	// (regla del enunciado: el siguiente grupo toma el puesto que sigue).
	estados := []Estado{
		{UsuarioID: "a", Fichas: 1000},
		{UsuarioID: "b", Fichas: 1000},
		{UsuarioID: "c", Fichas: 1000},
		{UsuarioID: "d", Fichas: 500},
	}
	r := ConstruirRanking("e1", estados, 1000, 50, 0, instante)
	for i := 0; i < 3; i++ {
		if r.Filas[i].Puesto != 1 {
			t.Fatalf("fila %d deberia ser puesto 1: %+v", i, r.Filas[i])
		}
	}
	if r.Filas[3].Puesto != 2 {
		t.Fatalf("tras 3 empates en 1.º, el siguiente es el 2.º, tengo %d", r.Filas[3].Puesto)
	}
}

func TestElegibilidadYBarraDeAvance(t *testing.T) {
	// Minimo = 50 % de 1000 = 500 fichas apostadas.
	estados := []Estado{
		{UsuarioID: "justo", Fichas: 500, Apostado: 500}, // elegible, 100 %
		{UsuarioID: "medio", Fichas: 800, Apostado: 250}, // no elegible, 50 %
		{UsuarioID: "cero", Fichas: 1000, Apostado: 0},   // no elegible, 0 %
		{UsuarioID: "sobra", Fichas: 200, Apostado: 900}, // elegible, tope 100 %
	}
	r := ConstruirRanking("e1", estados, 1000, 50, 0, instante)
	if r.MinApostado != 500 {
		t.Fatalf("min apostado: quiero 500, tengo %d", r.MinApostado)
	}
	porUsuario := map[string]FilaRanking{}
	for _, f := range r.Filas {
		porUsuario[f.UsuarioID] = f
	}
	if !porUsuario["justo"].Elegible || porUsuario["justo"].AvancePct != 100 {
		t.Fatalf("justo deberia ser elegible al 100%%: %+v", porUsuario["justo"])
	}
	if porUsuario["medio"].Elegible || porUsuario["medio"].AvancePct != 50 {
		t.Fatalf("medio deberia estar al 50%% y no elegible: %+v", porUsuario["medio"])
	}
	if porUsuario["cero"].Elegible || porUsuario["cero"].AvancePct != 0 {
		t.Fatalf("cero deberia estar al 0%%: %+v", porUsuario["cero"])
	}
	if !porUsuario["sobra"].Elegible || porUsuario["sobra"].AvancePct != 100 {
		t.Fatalf("sobra deberia ser elegible con barra topada en 100%%: %+v", porUsuario["sobra"])
	}
}

func TestMinApostadoRedondeaHaciaArriba(t *testing.T) {
	// 50 % de 999 = 499.5 -> exige 500 (techo).
	if got := minApostadoPara(999, 50); got != 500 {
		t.Fatalf("techo de 499.5 deberia ser 500, tengo %d", got)
	}
	// 50 % de 1000 = 500 exacto.
	if got := minApostadoPara(1000, 50); got != 500 {
		t.Fatalf("500 exacto, tengo %d", got)
	}
}
