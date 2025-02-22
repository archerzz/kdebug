package aadssh

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	msal "github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

// prepareRequestData prepares AAD token request data
func prepareRequestData(sshPubKey ssh.PublicKey) (map[string]string, error) {
	exponentString, modulusString, err := parseSSHPublicKey(sshPubKey)
	if err != nil {
		return nil, fmt.Errorf("Fail to parse SSH public key due to: %+v", err)
	}

	hash := sha256.New()
	hash.Write([]byte(modulusString))
	hash.Write([]byte(exponentString))
	keyId := hex.EncodeToString(hash.Sum(nil))
	jwk := map[string]string{
		"kty": "RSA",
		"n":   modulusString,
		"e":   exponentString,
		"kid": keyId,
	}
	jwkJson, err := json.Marshal(jwk)
	if err != nil {
		return nil, fmt.Errorf("Fail to parse encode JWK payload due to: %+v", err)
	}

	data := map[string]string{
		"token_type": "ssh-cert",
		"req_cnf":    string(jwkJson),
		"key_id":     keyId,
	}

	return data, nil
}

// acquireCertificate acquires SSH certificate from AAD
func acquireCertificate(useAzureCLI bool, sshPubKey ssh.PublicKey) (*ssh.Certificate, error) {
	// Prepare token request data
	data, err := prepareRequestData(sshPubKey)
	if err != nil {
		return nil, fmt.Errorf("Fail to prepare request data: %+v", err)
	}
	log.WithFields(log.Fields{
		"data": data,
	}).Debug("Token request data")

	// Request token
	httpClient := &http.Client{
		Timeout:   time.Minute,
		Transport: &Transport{data: data},
	}
	client, err := msal.New(AzureCLIClientId,
		msal.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("Fail to create MSAL client: %+v", err)
	}

	scopes := []string{
		"https://pas.windows.net/CheckMyAccess/Linux/.default",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var authResult msal.AuthResult
	if useAzureCLI {
		authResult, err = acquireTokenByAzureCLI(ctx, scopes, data)
	} else {
		authResult, err = client.AcquireTokenInteractive(ctx, scopes)
	}
	if err != nil {
		return nil, fmt.Errorf("Fail to create acquire AAD token: %+v", err)
	}

	log.WithFields(log.Fields{"authResult": fmt.Sprintf("%+v", authResult)}).Debug("Got AAD auth result")

	sshCertData, err := base64.StdEncoding.DecodeString(authResult.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("Fail to base64 decode SSH certificate: %+v", err)
	}
	sshPub, err := ssh.ParsePublicKey(sshCertData)
	if err != nil {
		return nil, fmt.Errorf("Fail to parse SSH certificate: %+v", err)
	}
	sshCert, ok := sshPub.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("Not a SSH certificate")
	}

	validBefore := time.Unix(int64(sshCert.ValidBefore), 0)
	log.WithFields(log.Fields{"validBefore": validBefore}).Info("Got SSH certificate. Re-run this command to obtain a new one after it expires.")

	return sshCert, nil
}
