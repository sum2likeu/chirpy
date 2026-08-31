package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sum2likeu/chirpy/internal/auth"

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
	secret := os.Getenv("SECRET")
	polka := os.Getenv("POLKA_KEY")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println(err)
	}
	dbQueries := database.New(db)
	apiCfg := apiConfig{
		db:       dbQueries,
		platform: platform,
		secret:   secret,
		polka:    polka,
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
	servHandler.HandleFunc("POST /api/login", apiCfg.handlerLogin)
	servHandler.HandleFunc("POST /api/refresh", apiCfg.handlerRefresh)
	servHandler.HandleFunc("POST /api/revoke", apiCfg.handlerRevoke)
	servHandler.HandleFunc("PUT /api/users", apiCfg.handlerUpdateInfo)
	servHandler.HandleFunc("DELETE /api/chirps/{chirpID}", apiCfg.handlerDeleteChirp)
	servHandler.HandleFunc("POST /api/polka/webhooks", apiCfg.handlerChirpyRed)
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
	secret         string
	polka          string
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
		Body string `json:"body"`
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
	authtoken, err := auth.GetBearerToken(r.Header)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	authuser, err := auth.ValidateJWT(authtoken, cfg.secret)

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "Unauthorized")
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
		UserID: authuser,
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
	w.WriteHeader(code)
	w.Write(dat)
}
func (cfg *apiConfig) handlerUserByEmail(w http.ResponseWriter, r *http.Request) {
	type useremail struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	params := useremail{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "something went wrong")
		return
	}
	hash, err := auth.HashPassword(params.Password)
	if err != nil {
		respondWithError(w, 500, "couldn't hash password")
		return
	}
	userresp, err := cfg.db.CreateUser(r.Context(), database.CreateUserParams{
		Email:          params.Email,
		HashedPassword: hash,
	})
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
		IsRed  bool      `json:"is_chirpy_red"`
	}
	userinfo := user{
		ID:     userresp.ID,
		Create: userresp.CreatedAt,
		Update: userresp.UpdatedAt,
		Email:  userresp.Email,
		IsRed:  userresp.IsChirpyRed,
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
	authorIDString := r.URL.Query().Get("author_id")
	sortby := r.URL.Query().Get("sort")
	if authorIDString != "" {
		authorID, err := uuid.Parse(authorIDString)

		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 401, "something went wrong")
			return
		}
		if authorIDString != "" {
			authchirpslice, err := cfg.db.GetChirpsByAuthor(r.Context(), authorID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				respondWithError(w, 500, "something went wrong")
				return
			}
			chirps := []chirp{}
			for _, dbChirps := range authchirpslice {
				chirps = append(chirps, chirp{
					Id:         dbChirps.ID,
					Created_at: dbChirps.CreatedAt,
					Updated_at: dbChirps.UpdatedAt,
					Body:       dbChirps.Body,
					User_id:    dbChirps.UserID,
				})
			}
			if sortby == "desc" {
				sort.Slice(chirps, func(i, j int) bool {
					return chirps[i].Created_at.After(chirps[j].Created_at)
				})
			} else {
				sort.Slice(chirps, func(i, j int) bool {
					return chirps[i].Created_at.Before(chirps[j].Created_at)
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
	}
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
	if sortby == "desc" {
		sort.Slice(chirps, func(i, j int) bool {
			return chirps[i].Created_at.After(chirps[j].Created_at)
		})
	} else {
		sort.Slice(chirps, func(i, j int) bool {
			return chirps[i].Created_at.Before(chirps[j].Created_at)
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
func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request) {
	type useremail struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	params := useremail{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&params)

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "params")
		return
	}
	userinfo, err := cfg.db.GetHashByEmail(r.Context(), params.Email)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	check, err := auth.CheckPasswordHash(params.Password, userinfo.HashedPassword)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	if check == false {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}

	if check == true {
		seconds := time.Duration(3600) * time.Second

		expire := time.Now().Add(time.Duration(1440) * time.Hour)
		refreshTokenString := auth.MakeRefreshToken()
		err := cfg.db.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{
			Token:     refreshTokenString,
			UserID:    userinfo.ID,
			ExpiresAt: expire,
		})
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 500, "token creation")
			return
		}
		tokenstring, err := auth.MakeJWT(userinfo.ID, cfg.secret, seconds)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 500, "token creation")
			return
		}
		type user struct {
			ID            uuid.UUID `json:"id"`
			Create        time.Time `json:"created_at"`
			Update        time.Time `json:"updated_at"`
			Email         string    `json:"email"`
			IsRed         bool      `json:"is_chirpy_red"`
			Token         string    `json:"token"`
			Refresh_token string    `json:"refresh_token"`
		}
		userinfo := user{
			ID:            userinfo.ID,
			Create:        userinfo.CreatedAt,
			Update:        userinfo.UpdatedAt,
			Email:         userinfo.Email,
			IsRed:         userinfo.IsChirpyRed,
			Token:         tokenstring,
			Refresh_token: refreshTokenString,
		}
		dat, err := json.Marshal(userinfo)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 500, "marshal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(dat)
	}
}
func (cfg *apiConfig) handlerRefresh(w http.ResponseWriter, r *http.Request) {
	seconds := time.Duration(3600) * time.Second
	tokenstring, err := auth.GetBearerToken(r.Header)
	type tokenreturn struct {
		Token string `json:"token"`
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	tokeninfo, err := cfg.db.GetToken(r.Context(), tokenstring)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	if tokeninfo.RevokedAt.Valid == true || time.Now().Compare(tokeninfo.ExpiresAt) >= 0 {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return

	} else {
		madetoken, err := auth.MakeJWT(tokeninfo.UserID, cfg.secret, seconds)
		currenttoken := tokenreturn{
			Token: madetoken,
		}
		dat, err := json.Marshal(currenttoken)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 500, "marshal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(dat)
		return
	}

}
func (cfg *apiConfig) handlerRevoke(w http.ResponseWriter, r *http.Request) {
	tokenstring, err := auth.GetBearerToken(r.Header)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	cfg.db.RevokeToken(r.Context(), tokenstring)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(204)
}
func (cfg *apiConfig) handlerUpdateInfo(w http.ResponseWriter, r *http.Request) {
	tokenstring, err := auth.GetBearerToken(r.Header)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	type emailpass struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	params := emailpass{}
	decoder := json.NewDecoder(r.Body)
	err = decoder.Decode(&params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	tokenuserid, err := auth.ValidateJWT(tokenstring, cfg.secret)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	hpass, err := auth.HashPassword(params.Password)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	cfg.db.UpdateUser(r.Context(), database.UpdateUserParams{
		Email:          params.Email,
		HashedPassword: hpass,
		ID:             tokenuserid,
	})
	hashinfo, err := cfg.db.GetHashByEmail(r.Context(), params.Email)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	type user struct {
		ID             uuid.UUID `json:"id"`
		Create         time.Time `json:"created_at"`
		Update         time.Time `json:"updated_at"`
		Email          string    `json:"email"`
		IsRed          bool      `json:"is_chirpy_red"`
		HashedPassword string    `json:"password_hash"`
	}
	userinfo := user{
		ID:             hashinfo.ID,
		Create:         hashinfo.CreatedAt,
		Update:         hashinfo.UpdatedAt,
		Email:          hashinfo.Email,
		IsRed:          hashinfo.IsChirpyRed,
		HashedPassword: hpass,
	}
	dat, err := json.Marshal(userinfo)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 500, "marshal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(dat)

}
func (cfg *apiConfig) handlerDeleteChirp(w http.ResponseWriter, r *http.Request) {
	chirpidstring := r.PathValue("chirpID")
	chirpid, err := uuid.Parse(chirpidstring)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	tokenstring, err := auth.GetBearerToken(r.Header)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	tokenuserid, err := auth.ValidateJWT(tokenstring, cfg.secret)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	userid, err := cfg.db.GetChirpUserID(r.Context(), chirpid)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 404, "something went wrong")
		return
	}
	if tokenuserid == userid {
		cfg.db.DeleteChirp(r.Context(), chirpid)
		w.WriteHeader(204)
		return
	}
	w.WriteHeader(403)

}
func (cfg *apiConfig) handlerChirpyRed(w http.ResponseWriter, r *http.Request) {
	apikey, err := auth.GetAPIKey(r.Header)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	if apikey != cfg.polka {
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			respondWithError(w, 401, "something went wrong")
			return
		}
	}
	type dataThing struct {
		UserID string `json:"user_id"`
	}
	type webhookinfo struct {
		Event string    `json:"event"`
		Data  dataThing `json:"data"`
	}
	params := webhookinfo{}
	decoder := json.NewDecoder(r.Body)
	err = decoder.Decode(&params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	if params.Event != "user.upgraded" {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 204, "something went wrong")
		return
	}
	id, err := uuid.Parse(params.Data.UserID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 401, "something went wrong")
		return
	}
	err = cfg.db.UpdateChirpyRed(r.Context(), id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		respondWithError(w, 404, "something went wrong")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(204)
}
