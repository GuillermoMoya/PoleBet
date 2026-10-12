package mercados

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arquisoft/motor/internal/ws"
)

const admin = "00000000-0000-0000-0000-000000000001"

var inicio = time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)

// --- Dobles de prueba ---

type repoFalso struct {
	mu       sync.Mutex
	carreras []string // la ultima es la mas reciente
	mercados []Mercado
	sig      int
	errCerr  error
}

func (r *repoFalso) CarreraMasReciente(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.carreras) == 0 {
		return "", ErrSinCarrera
	}
	return r.carreras[len(r.carreras)-1], nil
}

func (r *repoFalso) ExisteCarrera(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.carreras {
		if c == id {
			return true, nil
		}
	}
	return false, nil
}

func (r *repoFalso) Insertar(_ context.Context, m Mercado) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.mercados { // imita el indice unico parcial
		if x.Estado == EstadoAbierto {
			return "", ErrYaHayMercadoAbierto
		}
	}
	r.sig++
	m.ID = fmt.Sprintf("m%d", r.sig)
	r.mercados = append(r.mercados, m)
	return m.ID, nil
}

func (r *repoFalso) Actual(context.Context) (Mercado, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.mercados) == 0 {
		return Mercado{}, ErrNoHayMercado
	}
	return r.mercados[len(r.mercados)-1], nil
}

func (r *repoFalso) Pendientes(context.Context) ([]Mercado, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Mercado
	for _, m := range r.mercados {
		if m.Estado == EstadoAbierto || m.Estado == EstadoCerrado {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *repoFalso) Cerrar(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.errCerr != nil {
		return false, r.errCerr
	}
	for i := range r.mercados {
		if r.mercados[i].ID == id && r.mercados[i].Estado == EstadoAbierto {
			r.mercados[i].Estado = EstadoCerrado
			return true, nil
		}
	}
	return false, nil
}

func (r *repoFalso) estado(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.mercados {
		if m.ID == id {
			return m.Estado
		}
	}
	return ""
}

type difusorFalso struct {
	mu       sync.Mutex
	mensajes []ws.Mensaje
}

func (d *difusorFalso) Difundir(m ws.Mensaje) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mensajes = append(d.mensajes, m)
	return nil
}

func (d *difusorFalso) tipos() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, m := range d.mensajes {
		out = append(out, m.Type)
	}
	return out
}

func (d *difusorFalso) ultimo() ws.Mensaje {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mensajes[len(d.mensajes)-1]
}

// agenda reemplaza a time.AfterFunc: guarda las tareas y la prueba decide
// cuando correrlas.
type tarea struct {
	d time.Duration
	f func()
}

type agenda struct {
	mu     sync.Mutex
	tareas []tarea
}

func (a *agenda) despues(d time.Duration, f func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tareas = append(a.tareas, tarea{d, f})
}

// correr saca y ejecuta la tarea mas antigua; devuelve con que espera se programo.
func (a *agenda) correr(t *testing.T) time.Duration {
	t.Helper()
	a.mu.Lock()
	if len(a.tareas) == 0 {
		a.mu.Unlock()
		t.Fatal("no hay tareas programadas")
	}
	x := a.tareas[0]
	a.tareas = a.tareas[1:]
	a.mu.Unlock()
	x.f()
	return x.d
}

func (a *agenda) pendientes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.tareas)
}

type liquidadorFalso struct {
	mu        sync.Mutex
	liquidado []string
}

func (l *liquidadorFalso) Liquidar(_ context.Context, m Mercado) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.liquidado = append(l.liquidado, m.ID)
	return nil
}

type entorno struct {
	svc   *Servicio
	repo  *repoFalso
	dif   *difusorFalso
	ag    *agenda
	liq   *liquidadorFalso
	ahora time.Time
}

func nuevo(t *testing.T) *entorno {
	t.Helper()
	e := &entorno{
		repo:  &repoFalso{carreras: []string{"2023_spa", "2024_monza"}},
		dif:   &difusorFalso{},
		ag:    &agenda{},
		liq:   &liquidadorFalso{},
		ahora: inicio,
	}
	e.svc = NuevoServicio(e.repo, e.dif, 30*time.Second, 60*time.Second)
	e.svc.Ahora = func() time.Time { return e.ahora }
	e.svc.Despues = e.ag.despues
	e.svc.Liquidador = e.liq
	return e
}

func peticionValida() Peticion {
	return Peticion{Pregunta: "¿Quién irá primero al terminar el minuto?", Opciones: []string{"VER", "NOR", "LEC"}}
}

// --- Abrir ---

func TestAbrirCreaMercadoAbiertoYLoDifunde(t *testing.T) {
	e := nuevo(t)

	v, err := e.svc.Abrir(context.Background(), admin, peticionValida())
	if err != nil {
		t.Fatal(err)
	}
	if v.Estado != EstadoAbierto || v.CarreraID != "2024_monza" {
		t.Errorf("vista = %+v", v)
	}
	if !v.AbreEn.Equal(inicio) || !v.CierraEn.Equal(inicio.Add(30*time.Second)) || !v.ObservaHasta.Equal(inicio.Add(90*time.Second)) {
		t.Errorf("tiempos: abre=%s cierra=%s observa=%s", v.AbreEn, v.CierraEn, v.ObservaHasta)
	}
	if !v.Ahora.Equal(inicio) {
		t.Errorf("ahora = %s, se esperaba la hora del servidor", v.Ahora)
	}
	if got := e.dif.tipos(); len(got) != 1 || got[0] != ws.TipoMercadoAbierto {
		t.Fatalf("difusiones = %v", got)
	}
	if p, ok := e.dif.ultimo().Payload.(Vista); !ok || p.ID != v.ID {
		t.Errorf("payload = %#v", e.dif.ultimo().Payload)
	}
	if e.repo.mercados[0].CreadoPor != admin {
		t.Errorf("creado_por = %q", e.repo.mercados[0].CreadoPor)
	}
	// El validador de Mongo exige tipo y el liquidador solo resuelve este valor.
	if e.repo.mercados[0].Tipo != TipoPilotoPrimero {
		t.Errorf("tipo = %q, se esperaba %q", e.repo.mercados[0].Tipo, TipoPilotoPrimero)
	}
	if e.ag.pendientes() != 1 || e.ag.tareas[0].d != 30*time.Second {
		t.Errorf("se esperaba el cierre programado en 30 s: %+v", e.ag.tareas)
	}
}

func TestAbrirLimpiaEspacios(t *testing.T) {
	e := nuevo(t)
	v, err := e.svc.Abrir(context.Background(), admin, Peticion{
		CarreraID: " 2023_spa ", Pregunta: "  ¿Quién gana?  ", Opciones: []string{" VER ", "NOR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.CarreraID != "2023_spa" || v.Pregunta != "¿Quién gana?" || v.Opciones[0] != "VER" {
		t.Errorf("vista = %+v", v)
	}
}

func TestAbrirValidaLaPeticion(t *testing.T) {
	largo := func(n int) string { return strings.Repeat("ñ", n) } // runas, no bytes
	casos := []struct {
		nombre   string
		p        Peticion
		contiene string
	}{
		{"sin pregunta", Peticion{Pregunta: "  ", Opciones: []string{"A", "B"}}, "pregunta"},
		{"pregunta larga", Peticion{Pregunta: largo(MaxLargoPregunta + 1), Opciones: []string{"A", "B"}}, "pregunta"},
		{"una opcion", Peticion{Pregunta: "¿?", Opciones: []string{"A"}}, "entre 2 y 10"},
		{"sin opciones", Peticion{Pregunta: "¿?"}, "entre 2 y 10"},
		{"once opciones", Peticion{Pregunta: "¿?", Opciones: strings.Split("a,b,c,d,e,f,g,h,i,j,k", ",")}, "entre 2 y 10"},
		{"opcion vacia", Peticion{Pregunta: "¿?", Opciones: []string{"A", " "}}, "vacia"},
		{"opcion larga", Peticion{Pregunta: "¿?", Opciones: []string{"A", largo(MaxLargoOpcion + 1)}}, "100"},
		{"opcion repetida", Peticion{Pregunta: "¿?", Opciones: []string{"VER", "ver "}}, "repetida"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			e := nuevo(t)
			_, err := e.svc.Abrir(context.Background(), admin, c.p)
			if !errors.Is(err, ErrPeticionInvalida) || !strings.Contains(err.Error(), c.contiene) {
				t.Errorf("error = %v, se esperaba uno de validacion con %q", err, c.contiene)
			}
			if len(e.repo.mercados) != 0 || len(e.dif.tipos()) != 0 {
				t.Error("una peticion invalida no debe crear ni difundir nada")
			}
		})
	}
}

func TestAbrirLimitesExactosSonValidos(t *testing.T) {
	e := nuevo(t)
	opciones := make([]string, MaxOpciones)
	for i := range opciones {
		opciones[i] = fmt.Sprintf("%d%s", i, strings.Repeat("x", MaxLargoOpcion-1))
	}
	_, err := e.svc.Abrir(context.Background(), admin, Peticion{Pregunta: strings.Repeat("p", MaxLargoPregunta), Opciones: opciones})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAbrirConCarreraInexistenteOSinCarreras(t *testing.T) {
	e := nuevo(t)
	p := peticionValida()
	p.CarreraID = "1999_nada"
	if _, err := e.svc.Abrir(context.Background(), admin, p); !errors.Is(err, ErrCarreraNoExiste) {
		t.Errorf("error = %v", err)
	}

	e = nuevo(t)
	e.repo.carreras = nil
	if _, err := e.svc.Abrir(context.Background(), admin, peticionValida()); !errors.Is(err, ErrSinCarrera) {
		t.Errorf("error = %v", err)
	}
}

func TestSoloUnMercadoAbiertoALaVez(t *testing.T) {
	e := nuevo(t)
	if _, err := e.svc.Abrir(context.Background(), admin, peticionValida()); err != nil {
		t.Fatal(err)
	}
	e.ahora = inicio.Add(29 * time.Second)
	if _, err := e.svc.Abrir(context.Background(), admin, peticionValida()); !errors.Is(err, ErrYaHayMercadoAbierto) {
		t.Errorf("error = %v", err)
	}
	if len(e.repo.mercados) != 1 {
		t.Errorf("mercados = %d", len(e.repo.mercados))
	}
}

func TestAbrirConcurrenteCreaUnoSolo(t *testing.T) {
	e := nuevo(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, ocupado := 0, 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.Abrir(context.Background(), admin, peticionValida())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrYaHayMercadoAbierto):
				ocupado++
			default:
				t.Errorf("error inesperado: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || ocupado != 9 {
		t.Errorf("aceptados=%d rechazados=%d, se esperaba 1 y 9", ok, ocupado)
	}
}

// Si el temporizador del mercado anterior aun no corrio pero su ventana ya
// paso segun el servidor, Abrir lo cierra y deja abrir el nuevo.
func TestAbrirCierraElAnteriorSiYaVencio(t *testing.T) {
	e := nuevo(t)
	v1, _ := e.svc.Abrir(context.Background(), admin, peticionValida())
	e.ahora = inicio.Add(30 * time.Second) // = cierra_en

	v2, err := e.svc.Abrir(context.Background(), admin, peticionValida())
	if err != nil {
		t.Fatal(err)
	}
	if e.repo.estado(v1.ID) != EstadoCerrado || v2.Estado != EstadoAbierto {
		t.Errorf("anterior=%s nuevo=%s", e.repo.estado(v1.ID), v2.Estado)
	}
	want := []string{ws.TipoMercadoAbierto, ws.TipoMercadoCerrado, ws.TipoMercadoAbierto}
	if got := e.dif.tipos(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("difusiones = %v, se esperaba %v", got, want)
	}

	// El temporizador viejo corre tarde: no debe volver a difundir el cierre.
	e.ag.correr(t)
	if n := len(e.dif.tipos()); n != 3 {
		t.Errorf("el cierre se difundio dos veces: %v", e.dif.tipos())
	}
}

// --- Ciclo: cierre y observacion ---

func TestCicloCompletoAbiertoCerradoLiquidar(t *testing.T) {
	e := nuevo(t)
	v, _ := e.svc.Abrir(context.Background(), admin, peticionValida())

	// 1. Llega cierra_en.
	e.ahora = v.CierraEn
	if d := e.ag.correr(t); d != 30*time.Second {
		t.Errorf("cierre programado a %s", d)
	}
	if e.repo.estado(v.ID) != EstadoCerrado {
		t.Fatalf("estado = %s", e.repo.estado(v.ID))
	}
	m := e.dif.ultimo()
	if m.Type != ws.TipoMercadoCerrado || m.Payload.(Vista).Estado != EstadoCerrado {
		t.Errorf("mensaje = %+v", m)
	}
	if len(e.liq.liquidado) != 0 {
		t.Error("no se debe liquidar antes de la observacion")
	}

	// 2. Termina la ventana de observacion (1 minuto).
	e.ahora = v.ObservaHasta
	if d := e.ag.correr(t); d != 60*time.Second {
		t.Errorf("fin de observacion programado a %s", d)
	}
	if len(e.liq.liquidado) != 1 || e.liq.liquidado[0] != v.ID {
		t.Errorf("liquidados = %v", e.liq.liquidado)
	}
	if e.ag.pendientes() != 0 {
		t.Errorf("quedaron %d tareas", e.ag.pendientes())
	}
}

func TestSinLiquidadorSoloRegistra(t *testing.T) {
	e := nuevo(t)
	e.svc.Liquidador = nil
	e.svc.Abrir(context.Background(), admin, peticionValida())
	e.ag.correr(t) // cierre
	e.ag.correr(t) // fin de observacion: no debe entrar en panico
}

func TestFalloAlCerrarNoDifunde(t *testing.T) {
	e := nuevo(t)
	e.svc.Abrir(context.Background(), admin, peticionValida())
	e.repo.errCerr = errors.New("mongo caido")
	e.ag.correr(t)
	if got := e.dif.tipos(); len(got) != 1 {
		t.Errorf("difusiones = %v", got)
	}
}

func TestApagadoCancelaTemporizadores(t *testing.T) {
	e := nuevo(t)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.svc.Iniciar(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.Abrir(context.Background(), admin, peticionValida())
	cancel()
	e.ag.correr(t)
	if e.repo.estado(v.ID) != EstadoAbierto {
		t.Error("tras apagar el motor el temporizador no debe tocar la BD")
	}
}

// --- Iniciar: retomar tras un reinicio ---

func TestIniciarRetomaMercadosPendientes(t *testing.T) {
	e := nuevo(t)
	e.repo.mercados = []Mercado{
		{ID: "vencido", Estado: EstadoAbierto, CierraEn: inicio.Add(-time.Second), ObservaHasta: inicio.Add(59 * time.Second)},
		{ID: "liquidado", Estado: EstadoLiquidado},
		{ID: "observando", Estado: EstadoCerrado, ObservaHasta: inicio.Add(20 * time.Second)},
		{ID: "vigente", Estado: EstadoAbierto, CierraEn: inicio.Add(10 * time.Second), ObservaHasta: inicio.Add(70 * time.Second)},
	}

	if err := e.svc.Iniciar(context.Background()); err != nil {
		t.Fatal(err)
	}

	// El vencido se cierra de inmediato (y se avisa a los conectados).
	if e.repo.estado("vencido") != EstadoCerrado {
		t.Errorf("vencido = %s", e.repo.estado("vencido"))
	}
	if got := e.dif.tipos(); len(got) != 1 || got[0] != ws.TipoMercadoCerrado {
		t.Errorf("difusiones = %v", got)
	}

	esperas := map[time.Duration]bool{}
	for e.ag.pendientes() > 0 {
		esperas[e.ag.correr(t)] = true
	}
	// 59 s: observacion del vencido; 20 s: la del que estaba observando;
	// 10 s: cierre del vigente (que a su vez programa su observacion, 70 s).
	for _, d := range []time.Duration{59 * time.Second, 20 * time.Second, 10 * time.Second, 70 * time.Second} {
		if !esperas[d] {
			t.Errorf("falta una tarea programada a %s; hay %v", d, esperas)
		}
	}
	if e.repo.estado("vigente") != EstadoCerrado {
		t.Errorf("vigente = %s", e.repo.estado("vigente"))
	}
	if len(e.liq.liquidado) != 3 {
		t.Errorf("liquidados = %v", e.liq.liquidado)
	}
}

// Con el temporizador real y ventanas cortas: el reloj del servidor cierra solo.
func TestTemporizadorRealCierraElMercado(t *testing.T) {
	repo := &repoFalso{carreras: []string{"2024_monza"}}
	dif := &difusorFalso{}
	svc := NuevoServicio(repo, dif, 30*time.Millisecond, 30*time.Millisecond)
	liq := &liquidadorFalso{}
	svc.Liquidador = liq

	v, err := svc.Abrir(context.Background(), admin, peticionValida())
	if err != nil {
		t.Fatal(err)
	}
	limite := time.Now().Add(2 * time.Second)
	for time.Now().Before(limite) {
		liq.mu.Lock()
		n := len(liq.liquidado)
		liq.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if repo.estado(v.ID) != EstadoCerrado {
		t.Errorf("estado = %s", repo.estado(v.ID))
	}
	want := ws.TipoMercadoAbierto + "," + ws.TipoMercadoCerrado
	if got := strings.Join(dif.tipos(), ","); got != want {
		t.Errorf("difusiones = %s", got)
	}
	liq.mu.Lock()
	defer liq.mu.Unlock()
	if len(liq.liquidado) != 1 {
		t.Errorf("liquidados = %v", liq.liquidado)
	}
}

// --- Handler ---

func montar(t *testing.T) (http.Handler, *entorno) {
	t.Helper()
	e := nuevo(t)
	mux := http.NewServeMux()
	sinFiltro := func(h http.Handler) http.Handler { return h }
	Register(mux, e.svc, sinFiltro, sinFiltro, func(r *http.Request) (string, bool) {
		u := r.Header.Get("X-Test-User")
		return u, u != ""
	})
	return mux, e
}

func pedir(h http.Handler, metodo, ruta, user, cuerpo string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(metodo, ruta, strings.NewReader(cuerpo))
	if user != "" {
		r.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandlerAbrir(t *testing.T) {
	valido := `{"pregunta":"¿Quién irá primero?","opciones":["VER","NOR"]}`
	casos := []struct {
		nombre, user, cuerpo string
		status               int
		contiene             string
	}{
		{"ok", admin, valido, 201, `"estado":"abierto"`},
		{"sin sesion", "", valido, 401, "no autorizado"},
		{"json roto", admin, `{`, 400, "cuerpo invalido"},
		{"campo desconocido", admin, `{"pregunta":"x","opciones":["A","B"],"estado":"liquidado"}`, 400, "cuerpo invalido"},
		{"validacion", admin, `{"pregunta":"x","opciones":["A"]}`, 400, "entre 2 y 10"},
		{"carrera inexistente", admin, `{"carrera_id":"nada","pregunta":"x","opciones":["A","B"]}`, 404, "carrera no existe"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h, _ := montar(t)
			w := pedir(h, http.MethodPost, "/mercados", c.user, c.cuerpo)
			if w.Code != c.status || !strings.Contains(w.Body.String(), c.contiene) {
				t.Errorf("status=%d cuerpo=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandlerAbrirDosVecesDa409(t *testing.T) {
	h, _ := montar(t)
	cuerpo := `{"pregunta":"¿Quién irá primero?","opciones":["VER","NOR"]}`
	pedir(h, http.MethodPost, "/mercados", admin, cuerpo)
	w := pedir(h, http.MethodPost, "/mercados", admin, cuerpo)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Ya hay un mercado abierto") {
		t.Errorf("status=%d cuerpo=%s", w.Code, w.Body.String())
	}
}

func TestHandlerActual(t *testing.T) {
	h, e := montar(t)

	w := pedir(h, http.MethodGet, "/mercados/actual", admin, "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "null" {
		t.Errorf("sin mercados: status=%d cuerpo=%s", w.Code, w.Body.String())
	}

	e.svc.Abrir(context.Background(), admin, peticionValida())
	w = pedir(h, http.MethodGet, "/mercados/actual", "jugador", "")
	var v Vista
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
		t.Fatalf("status=%d err=%v cuerpo=%s", w.Code, err, w.Body.String())
	}
	if v.Estado != EstadoAbierto || len(v.Opciones) != 3 || v.CierraEn.IsZero() || v.Ahora.IsZero() {
		t.Errorf("vista = %+v", v)
	}
}

// El JSON que recibe el front tiene los nombres acordados en el README.
func TestFormatoJSONDeLaVista(t *testing.T) {
	e := nuevo(t)
	v, _ := e.svc.Abrir(context.Background(), admin, peticionValida())
	b, _ := json.Marshal(v)
	for _, campo := range []string{`"id"`, `"carrera_id"`, `"pregunta"`, `"opciones"`, `"estado"`, `"abre_en"`, `"cierra_en"`, `"observa_hasta"`, `"ahora"`} {
		if !strings.Contains(string(b), campo) {
			t.Errorf("falta %s en %s", campo, b)
		}
	}
}
