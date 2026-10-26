// Package fakebooksy is an in-memory stand-in for Booksy's customer API,
// shaped after real responses (with invented data), for tests.
package fakebooksy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
)

const (
	Token       = "faketoken0123456789abcdefghijklm"
	BusinessID  = 3349
	VariantID   = 273164
	StafferID   = 131159
	AppointUID  = 123456789
	AppointVer  = 1700000000001
	DraftPrefix = "draft-"
	DefaultKey  = "web-e3d812bf-d7a2-445d-ab38-55589ae6a121"
)

// Server records calls and serves canned data. Slots is mutable so tests can
// open and close times between checks.
type Server struct {
	*httptest.Server
	mu    sync.Mutex
	Slots map[string][]string // date -> times
	// Key is the web api key the fake accepts and serves from its fake
	// booksy.com scripts. Change it to simulate Booksy rotating the key.
	Key     string
	WebHits int
	// Prepayment is what payments/calc reports; "0.00" unless a test sets it.
	Prepayment       string
	LastBookedStatus string
	Calls            []string
	Scheduled        int
	Cancelled        int
	drafts           map[string]map[string]any
	nextDraft        int
}

func New() *Server {
	s := &Server{
		Slots:      map[string][]string{},
		drafts:     map[string]map[string]any{},
		Key:        DefaultKey,
		Prepayment: "0.00",
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func (s *Server) CallLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Calls...)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, status int, code, desc, field string) {
	write(w, status, map[string]any{"errors": []map[string]any{{"code": code, "description": desc, "field": field}}})
}

func business() map[string]any {
	return map[string]any{
		"id": BusinessID, "name": "Barberia Test ", "url": "3349_barberia-test_barberia_53009_madrid",
		"description": "Una barbería de prueba",
		"location":    map[string]any{"coordinate": map[string]any{"latitude": 40.41, "longitude": -3.70}, "city": "Madrid", "address": "Calle Falsa, 1, 28012, Madrid"},
		"distance":    634, "reviews_rank": 4.86255, "reviews_count": 422, "primary_category": 45,
		"booking_policy": "Changes allowed up to 1 Horas before visit",
		"open_hours":     []map[string]any{{"day_of_week": 1, "open_from": "10:00", "open_till": "19:55"}, {"day_of_week": 0, "open_from": "10:00", "open_till": "14:00"}},
		"amenities":      []map[string]any{{"key": "wifi", "label": "Wi-Fi"}},
		"staff": []map[string]any{
			{"id": StafferID, "name": "Javier", "position": "Barbero", "visible": true, "active": true},
			{"id": 259670, "name": "Roy", "visible": true, "active": true},
			{"id": 1, "name": "Hidden", "visible": false, "active": true},
		},
		"service_categories": []map[string]any{
			{"id": 6051, "name": "Cortes", "services": []map[string]any{
				{"id": 46629, "name": "Corte de pelo", "active": true, "is_available_for_customer_booking": true, "staffer_id": []int{StafferID, 259670},
					"variants": []map[string]any{{"id": VariantID, "duration": 25, "price": 17.0, "type": "X", "service_price": "17,00 €"}}},
				{"id": 46633, "name": "Skinfade", "active": true, "variants": []map[string]any{{"id": 273072, "duration": 35, "price": 18.0, "type": "X"}}},
				{"id": 46640, "name": "Corte y barba", "active": true, "variants": []map[string]any{
					{"id": 300001, "duration": 40, "price": 25.0, "label": "corto"},
					{"id": 300002, "duration": 50, "price": 30.0, "label": "largo"},
				}},
				{"id": 46641, "name": "Retired", "active": false, "variants": []map[string]any{{"id": 300009, "duration": 5}}},
			}},
		},
	}
}

func appointment(status string) map[string]any {
	return map[string]any{
		"appointment_uid": AppointUID, "_version": AppointVer, "status": status,
		"booked_from": "2026-12-04T18:00", "booked_till": "2026-12-04T18:30", "timezone_name": "Europe/Madrid",
		"total": "11,00 €", "actions": map[string]any{"cancel": status == "A", "change": status == "A"},
		"business":    map[string]any{"id": BusinessID, "name": "Barberia Test", "url": "3349_barberia-test_barberia_53009_madrid"},
		"subbookings": []map[string]any{{"service": map[string]any{"name": "Corte de pelo", "variant": map[string]any{"id": VariantID}}, "staffer": map[string]any{"name": "Javier"}}},
	}
}

func (s *Server) draftJSON(id string) map[string]any {
	d := s.drafts[id]
	return map[string]any{"appointment": map[string]any{
		"id": id, "version": d["version"], "status": d["status"],
		"business":    map[string]any{"id": BusinessID, "name": "Barberia Test"},
		"booked_from": d["from"], "booked_till": d["till"], "duration": "25min", "total": "17,00 €",
		"core_id":     d["core_id"],
		"subbookings": []map[string]any{{"id": "sub-1", "booked_from": d["from"], "booked_till": d["till"], "service": map[string]any{"name": "Corte de pelo", "price": "17,00 €", "variant": map[string]any{"id": VariantID, "duration": "25min"}}, "staffer": d["staffer"]}},
	}}
}

func (s *Server) authed(r *http.Request) bool { return r.Header.Get("X-Access-Token") == Token }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/core/v2/customer_api")
	s.Calls = append(s.Calls, r.Method+" "+path)
	// A fake booksy.com: the home page loads an entry script, which imports
	// the chunk holding the key, like the real Nuxt bundle.
	switch r.URL.Path {
	case "/es-es/":
		s.WebHits++
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><script type="module" src="%s/assets/entry-1.js"></script><link rel="modulepreload" href="/assets/vendor-2.js"></head></html>`, s.URL)
		return
	case "/assets/entry-1.js":
		s.WebHits++
		fmt.Fprint(w, "import{a}from\"./vendor-2.js\";const l=()=>import(`./config-3.js`);")
		return
	case "/assets/vendor-2.js":
		s.WebHits++
		fmt.Fprint(w, "export const a=1;")
		return
	case "/assets/config-3.js":
		s.WebHits++
		fmt.Fprintf(w, "hn={DEVELOPMENT:`web-af3c6e80-8420-11e3-baa7-0800200c9a66`,PRODUCTION:`%s`}", s.Key)
		return
	}
	if r.Header.Get("X-Api-Key") != s.Key {
		apiErr(w, 403, "invalid", "Clave de API incorrecta.", "api_key")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	switch {
	case r.Method == "GET" && path == "/businesses/categories/":
		write(w, 200, map[string]any{"categories": []map[string]any{{"id": 39, "name": "Peluquería"}, {"id": 45, "name": "Barbería"}}})
	case r.Method == "GET" && path == "/businesses":
		write(w, 200, map[string]any{"businesses": []any{business()}, "businesses_count": 246})
	case r.Method == "GET" && path == fmt.Sprintf("/businesses/%d", BusinessID):
		write(w, 200, map[string]any{"business": business()})
	case r.Method == "GET" && strings.HasPrefix(path, "/businesses/") && !strings.Contains(path[len("/businesses/"):], "/"):
		apiErr(w, 404, "not_found", "Objeto solicitado no encontrado", "")
	case r.Method == "GET" && path == fmt.Sprintf("/businesses/%d/reviews", BusinessID):
		write(w, 200, map[string]any{"reviews_count": 1, "reviews_rank": 4.9, "reviews": []map[string]any{{"id": 1, "rank": 5, "review": "Genial", "user": map[string]any{"first_name": "Ana", "last_name": "P…"}, "verified": true, "created": "2026-10-25T11:40", "services": []map[string]any{{"name": "Corte de pelo"}}}}})
	case r.Method == "GET" && path == "/search/street_hints/resolve/":
		if r.URL.Query().Get("query") == "nowhere" {
			apiErr(w, 400, "address", "Dirección incorrecta", "address")
			return
		}
		write(w, 200, map[string]any{"match": map[string]any{"latitude": 39.46894, "longitude": -0.37686, "formatted_address": "València, Comunitat Valenciana"}, "location": map[string]any{"id": 58087}})
	case r.Method == "POST" && path == fmt.Sprintf("/me/businesses/%d/appointments/time_slots", BusinessID):
		var days []map[string]any
		for _, d := range sortedKeys(s.Slots) {
			var ss []map[string]string
			for _, t := range s.Slots[d] {
				ss = append(ss, map[string]string{"t": t, "p": ""})
			}
			days = append(days, map[string]any{"date": d, "slots": ss})
		}
		write(w, 200, map[string]any{"time_slots": days})

	case r.Method == "POST" && path == "/drafts/create":
		if r.Header.Get("X-Fingerprint") == "" {
			apiErr(w, 500, "unexpected", "Problema con la reserva. Prueba de nuevo.", "")
			return
		}
		s.nextDraft++
		id := fmt.Sprintf("%s%d", DraftPrefix, s.nextDraft)
		s.drafts[id] = map[string]any{"version": "v1", "status": "X", "from": "2026-10-27T11:25", "till": "2026-10-27T11:50", "core_id": nil, "staffer": nil}
		write(w, 201, s.draftJSON(id))
	case r.Method == "POST" && path == "/drafts/edit_appointment":
		if !s.authed(r) {
			apiErr(w, 403, "required", "Se requiere un token", "auth_header_token")
			return
		}
		s.nextDraft++
		id := fmt.Sprintf("%s%d", DraftPrefix, s.nextDraft)
		s.drafts[id] = map[string]any{"version": "v1", "status": "X", "from": "2026-12-04T18:00", "till": "2026-12-04T18:30", "core_id": nil, "staffer": nil}
		write(w, 201, s.draftJSON(id))
	case r.Method == "POST" && strings.HasPrefix(path, "/drafts/"):
		parts := strings.Split(strings.Trim(path, "/"), "/")
		d, ok := s.drafts[parts[1]]
		if !ok || len(parts) != 3 {
			apiErr(w, 404, "not_found", "no draft", "")
			return
		}
		switch parts[2] {
		case "change_timeslot":
			slot, _ := body["slot"].(map[string]any)
			date, _ := slot["d"].(string)
			t, _ := slot["t"].(string)
			if body["version"] != d["version"] || !contains(s.Slots[date], t) {
				apiErr(w, 400, "validation", "Problema con la reserva. Prueba de nuevo.", "")
				return
			}
			d["from"], d["till"], d["version"] = date+"T"+t, date+"T"+t, "v2"
		case "change_staffer":
			d["staffer"], d["version"] = map[string]any{"id": StafferID, "name": "Javier"}, "v3"
		case "add_customer_note":
			d["version"] = "v4"
		case "schedule":
			if !s.authed(r) {
				apiErr(w, 403, "required", "Se requiere un token de autorización para este endpoint.", "auth_header_token")
				return
			}
			// Like the real API: skipping payments/calc leaves it awaiting payment.
			status := "O"
			if d["priced"] == true {
				status = "A"
			}
			d["status"], d["core_id"] = status, AppointUID
			s.Scheduled++
			s.LastBookedStatus = status
		default:
			apiErr(w, 404, "not_found", "no action", "")
			return
		}
		write(w, 200, s.draftJSON(parts[1]))
	case r.Method == "GET" && strings.HasPrefix(path, "/drafts/") && strings.HasSuffix(path, "/confirmation"):
		id := strings.Split(strings.Trim(path, "/"), "/")[1]
		d := s.drafts[id]
		write(w, 200, map[string]any{"header": "Revisar y confirmar", "date": fmt.Sprint(d["from"]), "business_info": "Barberia Test • Calle Falsa, 1",
			"service_summary": []map[string]any{{"name": "Corte de pelo", "variant_name": "", "price": "17,00 €", "staffer_info": "Empleado:Javier", "time_info": "11:25 - 11:50"}},
			"total_summary":   map[string]any{"name": "Subtotal", "price": "17,00 €"}, "additional_info": map[string]any{"text": "Paga al finalizar."}})

	case strings.HasPrefix(path, "/me"):
		if !s.authed(r) {
			apiErr(w, 403, "required", "Se requiere un token de autorización para este endpoint.", "auth_header_token")
			return
		}
		s.serveMe(w, r, path, body)
	default:
		apiErr(w, 405, "not_allowed", "Método no permitido", "")
	}
}

func (s *Server) serveMe(w http.ResponseWriter, r *http.Request, path string, body map[string]any) {
	switch {
	case r.Method == "GET" && path == "/me":
		write(w, 200, map[string]any{"customer": map[string]any{"id": 42, "first_name": "Test", "last_name": "User", "email": "test@example.com", "latitude": 39.47, "longitude": -0.37, "city": "Valencia"}})
	case r.Method == "GET" && path == "/me/bookings":
		status := "A"
		if s.Cancelled > 0 {
			status = "C"
		}
		past := appointment("F")
		past["appointment_uid"], past["booked_from"] = 1, "2026-01-01T10:00"
		write(w, 200, map[string]any{"bookings": []any{appointment(status), past}, "bookings_count": 2})
	case r.Method == "GET" && path == fmt.Sprintf("/me/appointments/%d/", AppointUID):
		status := "A"
		if s.Cancelled > 0 {
			status = "C"
		}
		write(w, 200, map[string]any{"appointment": appointment(status)})
	case r.Method == "POST" && path == fmt.Sprintf("/me/appointments/%d/action/", AppointUID):
		if body["action"] != "cancel" || body["_version"] != float64(AppointVer) {
			apiErr(w, 400, "invalid", "bad version", "_version")
			return
		}
		s.Cancelled++
		write(w, 200, map[string]any{"appointment": appointment("C")})
	case r.Method == "POST" && path == "/me/payments/calc/":
		d, ok := s.drafts[fmt.Sprint(body["draft_id"])]
		if !ok {
			apiErr(w, 404, "not_found", "no draft", "draft_id")
			return
		}
		d["priced"] = true
		write(w, 200, map[string]any{"appointment": map[string]any{"status": "A"}, "appointment_payment": map[string]any{
			"prepayment": s.Prepayment, "prepayment_total": s.Prepayment, "cancellation_fee": "11.00", "cancellation_fee_total": "11.00", "to_pay_later": "17.00"}})
	case r.Method == "GET" && path == "/me/bookmarks/":
		write(w, 200, map[string]any{"businesses": []any{business()}})
	case (r.Method == "PUT" || r.Method == "DELETE") && path == fmt.Sprintf("/me/bookmarks/%d/", BusinessID):
		w.WriteHeader(204)
	case r.Method == "GET" && path == "/me/reviews/":
		write(w, 200, map[string]any{"reviews_count": 0, "reviews": []any{}})
	default:
		apiErr(w, 404, "not_found", "no", "")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
