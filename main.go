package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/sum2likeu/chirpy/internal/database"

	_ "github.com/lib/pq"
)

type chirp struct {
	Id         uuid.UUID `json:"id"`
	Created_at time.Time `json:"created_at"`
	Updated_at time.Time `json:"updated_at"`
	Body       string    `json:"body"`
	User_id    uuid.UUID `json:"user_id"`
}

func main() {

	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println(err)
	}
	dbQueries := database.New(db)
	apiCfg := apiConfig{
		db:       dbQueries,
		platform: platform,
	}
	servHandler := http.NewServeMux()
	s := &http.Server{
		Addr:           ":8080",
		Handler:        servHandler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	directory := http.Dir(".")
	handler := http.FileServer(directory)
	strippedHandler := http.StripPrefix("/app", handler)
	servHandler.Handle("/app/", apiCfg.middlewareMetricsInc(strippedHandler))
	servHandler.HandleFunc("GET /admin/metrics", apiCfg.handlerRequestCount)
	servHandler.HandleFunc("GET /api/healthz", handlerReadiness)
	servHandler.HandleFunc("GET /api/chirps", apiCfg.handlerGetChirps)
	servHandler.HandleFunc("GET /api/chirps/{chirpID}", apiCfg.handlerGetChirp)
	servHandler.HandleFunc("POST /admin/reset", apiCfg.handlerResetRequestCount)
	servHandler.HandleFunc("POST /api/chirps", apiCfg.handlerValidate)
	servHandler.HandleFunc("POST /api/users", apiCfg.handlerUserByEmail)
	log.Fatal(s.ListenAndServe())
}
func handlerReadiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	w.Write([]byte("OK"))
}
func (cfg *apiConfig) handlerRequestCount(w http.ResponseWriter, r *http.Request) {
	hits := cfg.fileserverHits.Load()
	fmt.Fprintf(w, `<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %v times!</p>
  </body>
</html>`, hits)
}

type apiConfig struct {
	fileserverHits atomic.Int32
	db             *database.Queries
	platform       string
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}
func (cfg *apiConfig) handlerResetRequestCount(w http.ResponseWriter, r *http.Request) {
	cfg.fileserverHits.Store(0)
	if cfg.platform == "dev" {

		err := cfg.db.DeleteUsers(r.Context())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 500, "something went wrong")
			return
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 403, "Forbidden")
	}

}
func (cfg *apiConfig) handlerValidate(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body   string    `json:"body"`
		UserID uuid.UUID `json:"user_id"`
	}
	type successResponse struct {
		Valid bool `json:"valid"`
	}
	type cleanResp struct {
		Clean string `json:"cleaned_body"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	if len(params.Body) > 140 {
		type errorResponse struct {
			Error string `json:"error"`
		}
		resp := errorResponse{
			Error: "something went wrong",
		}

		dat, err := json.Marshal(resp)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 500, "something went wrong")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write(dat)
		return
	}
	badwords := []string{"kerfuffle", "sharbert", "fornax"}
	filter := []string{}
	split := strings.Split(params.Body, " ")
	for _, value := range split {
		if slices.Contains(badwords, strings.ToLower(value)) {
			filter = append(filter, "****")
		} else {
			filter = append(filter, value)
		}
	}
	clean := cleanResp{
		Clean: strings.Join(filter, " "),
	}

	//suc := successResponse{
	//	Valid: true,
	//}
	chirpdat, err := cfg.db.CreateChirp(r.Context(), database.CreateChirpParams{
		Body:   clean.Clean,
		UserID: params.UserID,
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	jsondat := chirp{
		Id:         chirpdat.ID,
		Created_at: chirpdat.CreatedAt,
		Updated_at: chirpdat.UpdatedAt,
		Body:       chirpdat.Body,
		User_id:    chirpdat.UserID,
	}
	dat, err := json.Marshal(jsondat)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	w.Write(dat)
}
func respondWithError(w http.ResponseWriter, code int, e string) {
	w.WriteHeader(code)
	type errorResponse struct {
		Error string `json:"error"`
	}
	error := errorResponse{
		Error: e,
	}
	dat, err := json.Marshal(error)
	if err != nil {
		log.Printf("Error marshalling JSON: %s", err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	w.Write(dat)
}
func (cfg *apiConfig) handlerUserByEmail(w http.ResponseWriter, r *http.Request) {
	type useremail struct {
		Email string `json:"email"`
	}
	params := useremail{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	userresp, err := cfg.db.CreateUser(r.Context(), params.Email)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	type user struct {
		ID     uuid.UUID `json:"id"`
		Create time.Time `json:"created_at"`
		Update time.Time `json:"updated_at"`
		Email  string    `json:"email"`
	}
	userinfo := user{
		ID:     userresp.ID,
		Create: userresp.CreatedAt,
		Update: userresp.UpdatedAt,
		Email:  userresp.Email,
	}
	dat, err := json.Marshal(userinfo)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	w.Write(dat)
}
func (cfg *apiConfig) handlerGetChirps(w http.ResponseWriter, r *http.Request) {
	chirpslice, err := cfg.db.GetChirps(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	chirps := []chirp{}
	for _, dbChirps := range chirpslice {
		chirps = append(chirps, chirp{
			Id:         dbChirps.ID,
			Created_at: dbChirps.CreatedAt,
			Updated_at: dbChirps.UpdatedAt,
			Body:       dbChirps.Body,
			User_id:    dbChirps.UserID,
		})
	}
	dat, err := json.Marshal(chirps)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(dat)
}
func (cfg *apiConfig) handlerGetChirp(w http.ResponseWriter, r *http.Request) {
	chirpidstring := r.PathValue("chirpID")
	chirpid, err := uuid.Parse(chirpidstring)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 404, "something went wrong")
		return
	}
	singlechirp, err := cfg.db.GetChirp(r.Context(), chirpid)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 404, "something went wrong")
		return
	}
	chirpstruct := chirp{
		Id:         singlechirp.ID,
		Created_at: singlechirp.CreatedAt,
		Updated_at: singlechirp.UpdatedAt,
		Body:       singlechirp.Body,
		User_id:    singlechirp.UserID,
	}
	dat, err := json.Marshal(chirpstruct)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 404, "something went wrong")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(dat)
}
