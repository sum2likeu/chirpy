package auth

import (
	"encoding/hex"
	"net/http"
	"time"

	"errors"
	"strings"

	"crypto/rand"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func HashPassword(password string) (string, error) {
	passwordstring, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		return "", err
	}
	return passwordstring, nil
}
func CheckPasswordHash(password, hash string) (bool, error) {
	check, err := argon2id.ComparePasswordAndHash(password, hash)
	if err != nil {
		return false, err
	}
	return check, nil
}
func MakeJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error) {
	now := jwt.NewNumericDate(time.Now().UTC())
	exadd := time.Now().Add(expiresIn)
	expires := jwt.NewNumericDate(exadd.UTC())
	secretbyte := []byte(tokenSecret)
	claims := &jwt.RegisteredClaims{
		IssuedAt:  now,
		ExpiresAt: expires,
		Issuer:    "chirpy-access",
		Subject:   userID.String(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secretbyte)
}
func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	claim := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claim, func(*jwt.Token) (interface{}, error) {
		return []byte(tokenSecret), nil
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if ok {
		return uuid.Parse(claims.Subject)
	}
	return uuid.UUID{}, nil
}
func GetBearerToken(headers http.Header) (string, error) {
	unparsed := headers.Get("Authorization")
	parts := strings.Split(unparsed, " ")
	if len(parts) == 2 && parts[0] == "Bearer" {
		return parts[1], nil
	} else {
		return "", errors.New("key not valid")
	}
}
func MakeRefreshToken() string {
	data := make([]byte, 32)
	_, err := rand.Read(data)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
func GetAPIKey(headers http.Header) (string, error) {
	unparsed := headers.Get("Authorization")
	parts := strings.Split(unparsed, " ")
	if len(parts) == 2 && parts[0] == "ApiKey" {
		return parts[1], nil
	} else {
		return "", errors.New("key not valid")
	}
}
