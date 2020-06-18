/*
Copyright SecureKey Technologies Inc. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/hyperledger/aries-framework-go/pkg/didcomm/protocol/didexchange"
	"github.com/hyperledger/aries-framework-go/pkg/didcomm/protocol/mediator"
	"github.com/hyperledger/aries-framework-go/pkg/doc/verifiable"
	mocksvc "github.com/hyperledger/aries-framework-go/pkg/mock/didcomm/protocol/didexchange"
	mockroute "github.com/hyperledger/aries-framework-go/pkg/mock/didcomm/protocol/mediator"
	mockkms "github.com/hyperledger/aries-framework-go/pkg/mock/kms/legacykms"
	mockprovider "github.com/hyperledger/aries-framework-go/pkg/mock/provider"
	mockstore "github.com/hyperledger/aries-framework-go/pkg/mock/storage"
	"github.com/stretchr/testify/require"
	"github.com/trustbloc/edge-core/pkg/storage/memstore"
	mockstorage "github.com/trustbloc/edge-core/pkg/storage/mockstore"

	"github.com/trustbloc/edge-adapter/pkg/aries"
	"github.com/trustbloc/edge-adapter/pkg/profile/issuer"
)

func TestNew(t *testing.T) {
	t.Run("test new - success", func(t *testing.T) {
		c, err := New(&Config{
			AriesCtx:      getAriesCtx(),
			StoreProvider: memstore.NewProvider(),
		})
		require.NoError(t, err)

		require.Equal(t, 5, len(c.GetRESTHandlers()))
	})

	t.Run("test new - aries provider fail", func(t *testing.T) {
		c, err := New(&Config{AriesCtx: &mockprovider.Provider{}})
		require.Nil(t, c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to create aries did exchange client")
	})

	t.Run("test new - store fail", func(t *testing.T) {
		c, err := New(&Config{
			AriesCtx:      getAriesCtx(),
			StoreProvider: &mockstorage.Provider{ErrCreateStore: errors.New("error creating the store")},
		})
		require.Nil(t, c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "error creating the store")
	})

	t.Run("test get txn store - create store error", func(t *testing.T) {
		s, err := getTxnStore(&mockstorage.Provider{ErrCreateStore: errors.New("error creating the store")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "error creating the store")
		require.Nil(t, s)
	})

	t.Run("test get txn store - open store error", func(t *testing.T) {
		s, err := getTxnStore(&mockstorage.Provider{ErrOpenStoreHandle: errors.New("error opening the store")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "error opening the store")
		require.Nil(t, s)
	})
}

func TestCreateProfile(t *testing.T) {
	op, err := New(&Config{
		AriesCtx:      getAriesCtx(),
		StoreProvider: memstore.NewProvider(),
	})
	require.NoError(t, err)

	endpoint := profileEndpoint
	handler := getHandler(t, op, endpoint)

	t.Run("create profile - success", func(t *testing.T) {
		vReq := &issuer.ProfileData{
			ID:          uuid.New().String(),
			Name:        "test",
			CallbackURL: "http://issuer.example.com/callback",
		}

		vReqBytes, err := json.Marshal(vReq)
		require.NoError(t, err)

		rr := serveHTTP(t, handler.Handle(), http.MethodPost, endpoint, vReqBytes)

		require.Equal(t, http.StatusCreated, rr.Code)

		profileRes := &issuer.ProfileData{}
		err = json.Unmarshal(rr.Body.Bytes(), &profileRes)
		require.NoError(t, err)
		require.Equal(t, vReq, profileRes)
	})

	t.Run("create profile - invalid request", func(t *testing.T) {
		rr := serveHTTP(t, handler.Handle(), http.MethodPost, endpoint, []byte("invalid-json"))

		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "invalid request")
	})

	t.Run("create profile - error", func(t *testing.T) {
		vReq := &issuer.ProfileData{}

		vReqBytes, err := json.Marshal(vReq)
		require.NoError(t, err)

		rr := serveHTTP(t, handler.Handle(), http.MethodPost, endpoint, vReqBytes)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "failed to create profile: missing profile id")
	})
}

func TestGetProfile(t *testing.T) {
	op, err := New(&Config{
		AriesCtx:      getAriesCtx(),
		StoreProvider: memstore.NewProvider(),
	})
	require.NoError(t, err)

	endpoint := getProfileEndpoint
	handler := getHandler(t, op, endpoint)

	urlVars := make(map[string]string)

	t.Run("get profile - success", func(t *testing.T) {
		vReq := &issuer.ProfileData{
			ID:          "test",
			Name:        "Issuer Profile",
			CallbackURL: "http://issuer.example.com/cb",
		}

		err := op.profileStore.SaveProfile(vReq)
		require.NoError(t, err)

		urlVars[idPathParam] = vReq.ID

		rr := serveHTTPMux(t, handler, endpoint, nil, urlVars)

		require.Equal(t, http.StatusOK, rr.Code)

		profileRes := &issuer.ProfileData{}
		err = json.Unmarshal(rr.Body.Bytes(), &profileRes)
		require.NoError(t, err)
		require.Equal(t, vReq.ID, profileRes.ID)
	})

	t.Run("get profile - no data found", func(t *testing.T) {
		urlVars[idPathParam] = "invalid-name"

		rr := serveHTTPMux(t, handler, endpoint, nil, urlVars)

		fmt.Println(rr.Body.String())
		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "store does not have a value associated with this key")
	})
}

func TestConnectWallet(t *testing.T) {
	uiEndpoint := "/ui"
	profileID := "test-1"
	endpoint := walletConnectEndpoint
	urlVars := make(map[string]string)

	t.Run("test connect wallet - success", func(t *testing.T) {
		c, err := New(&Config{
			AriesCtx:      getAriesCtx(),
			StoreProvider: memstore.NewProvider(),
			UIEndpoint:    uiEndpoint,
		})
		require.NoError(t, err)

		data := &issuer.ProfileData{
			ID:          profileID,
			Name:        "Issuer Profile 1",
			CallbackURL: "http://issuer.example.com/cb",
		}
		err = c.profileStore.SaveProfile(data)
		require.NoError(t, err)

		walletConnectHandler := getHandler(t, c, endpoint)

		urlVars[idPathParam] = profileID

		rr := serveHTTPMux(t, walletConnectHandler, walletConnectEndpoint, nil, urlVars)

		require.Equal(t, http.StatusFound, rr.Code)
		require.Contains(t, rr.Header().Get("Location"), uiEndpoint)
	})

	t.Run("test connect wallet - profile doesn't exists", func(t *testing.T) {
		c, err := New(&Config{
			AriesCtx:      getAriesCtx(),
			StoreProvider: memstore.NewProvider(),
			UIEndpoint:    uiEndpoint,
		})
		require.NoError(t, err)

		walletConnectHandler := getHandler(t, c, endpoint)

		urlVars[idPathParam] = profileID

		rr := serveHTTPMux(t, walletConnectHandler, walletConnectEndpoint, nil, urlVars)

		fmt.Println(rr.Body.String())
		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "store does not have a value associated with this key")
	})

	t.Run("test connect wallet - txn data store error", func(t *testing.T) {
		c, err := New(&Config{
			AriesCtx:      getAriesCtx(),
			StoreProvider: memstore.NewProvider(),
			UIEndpoint:    uiEndpoint,
		})
		require.NoError(t, err)

		data := &issuer.ProfileData{
			ID:          profileID,
			Name:        "Issuer Profile 1",
			CallbackURL: "http://issuer.example.com/cb",
		}
		err = c.profileStore.SaveProfile(data)
		require.NoError(t, err)

		walletConnectHandler := getHandler(t, c, endpoint)

		urlVars[idPathParam] = profileID

		c.txnStore = &mockstorage.MockStore{
			Store:  make(map[string][]byte),
			ErrPut: errors.New("error inserting data"),
		}

		rr := serveHTTPMux(t, walletConnectHandler, walletConnectEndpoint, nil, urlVars)

		fmt.Println(rr.Body.String())
		require.Equal(t, http.StatusInternalServerError, rr.Code)
		require.Contains(t, rr.Body.String(), "failed to create txn")
	})
}

func TestValidateWalletResponse(t *testing.T) {
	c, err := New(&Config{
		AriesCtx:      getAriesCtx(),
		StoreProvider: memstore.NewProvider(),
	})
	require.NoError(t, err)

	handler := getHandler(t, c, validateConnectResponseEndpoint)

	vReq := &WalletConnect{
		Resp: []byte(""),
	}

	vReqBytes, err := json.Marshal(vReq)
	require.NoError(t, err)

	t.Run("test validate response - success", func(t *testing.T) {
		txnID, err := c.createTxn("profile1")
		require.NoError(t, err)

		req := &WalletConnect{
			Resp: getTestVP(t),
		}

		reqBytes, err := json.Marshal(req)
		require.NoError(t, err)

		rr := serveHTTP(t, handler.Handle(), http.MethodPost,
			validateConnectResponseEndpoint+"?"+txnIDQueryParam+"="+txnID, reqBytes)

		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("test validate response - missing cookie", func(t *testing.T) {
		rr := serveHTTP(t, handler.Handle(), http.MethodPost, validateConnectResponseEndpoint, vReqBytes)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "failed to get txnID from the url")
	})

	t.Run("test validate response - invalid req", func(t *testing.T) {
		txnID := "invalid-txn-id"

		rr := serveHTTP(t, handler.Handle(), http.MethodPost,
			validateConnectResponseEndpoint+"?"+txnIDQueryParam+"="+txnID, []byte("invalid-request"))

		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "invalid request")
	})

	t.Run("test validate response - invalid txn id", func(t *testing.T) {
		txnID := "invalid-txn-id"

		rr := serveHTTP(t, handler.Handle(), http.MethodPost,
			validateConnectResponseEndpoint+"?"+txnIDQueryParam+"="+txnID, vReqBytes)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "txn data not found")
	})

	t.Run("test validate response - invalid vp", func(t *testing.T) {
		txnID, err := c.createTxn("profile1")
		require.NoError(t, err)

		rr := serveHTTP(t, handler.Handle(), http.MethodPost,
			validateConnectResponseEndpoint+"?"+txnIDQueryParam+"="+txnID, vReqBytes)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "failed to validate presentation")
	})
}

func TestGenerateInvitation(t *testing.T) {
	t.Run("test new - success", func(t *testing.T) {
		c, err := New(&Config{
			AriesCtx:      getAriesCtx(),
			StoreProvider: memstore.NewProvider(),
		})
		require.NoError(t, err)

		generateInvitationHandler := getHandler(t, c, generateInvitationEndpoint)

		rr := serveHTTP(t, generateInvitationHandler.Handle(), http.MethodGet, generateInvitationEndpoint, nil)

		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("test new - error", func(t *testing.T) {
		ariesCtx := &mockprovider.Provider{
			TransientStorageProviderValue: mockstore.NewMockStoreProvider(),
			StorageProviderValue:          mockstore.NewMockStoreProvider(),
			ServiceMap: map[string]interface{}{
				didexchange.DIDExchange: &mocksvc.MockDIDExchangeSvc{},
				mediator.Coordination:   &mockroute.MockMediatorSvc{},
			},
			LegacyKMSValue:       &mockkms.CloseableKMS{CreateKeyErr: errors.New("key generation error")},
			ServiceEndpointValue: "endpoint",
		}

		c, err := New(&Config{
			AriesCtx:      ariesCtx,
			StoreProvider: memstore.NewProvider(),
		})
		require.NoError(t, err)

		generateInvitationHandler := getHandler(t, c, generateInvitationEndpoint)

		rr := serveHTTP(t, generateInvitationHandler.Handle(), http.MethodGet, generateInvitationEndpoint, nil)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
		require.Contains(t, rr.Body.String(), "failed to create invitation")
	})
}

func getAriesCtx() aries.CtxProvider {
	return &mockprovider.Provider{
		TransientStorageProviderValue: mockstore.NewMockStoreProvider(),
		StorageProviderValue:          mockstore.NewMockStoreProvider(),
		ServiceMap: map[string]interface{}{
			didexchange.DIDExchange: &mocksvc.MockDIDExchangeSvc{},
			mediator.Coordination:   &mockroute.MockMediatorSvc{},
		},
		LegacyKMSValue:       &mockkms.CloseableKMS{CreateEncryptionKeyValue: "sample-key"},
		ServiceEndpointValue: "endpoint",
	}
}

func getHandler(t *testing.T, op *Operation, lookup string) Handler {
	return getHandlerWithError(t, op, lookup)
}

func getHandlerWithError(t *testing.T, op *Operation, lookup string) Handler {
	return handlerLookup(t, op, lookup)
}

func handlerLookup(t *testing.T, op *Operation, lookup string) Handler {
	handlers := op.GetRESTHandlers()
	require.NotEmpty(t, handlers)

	for _, h := range handlers {
		if h.Path() == lookup {
			return h
		}
	}

	require.Fail(t, "unable to find handler")

	return nil
}

func serveHTTP(t *testing.T, handler http.HandlerFunc, method, path string, req []byte) *httptest.ResponseRecorder {
	httpReq, err := http.NewRequest(
		method,
		path,
		bytes.NewBuffer(req),
	)
	require.NoError(t, err)

	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, httpReq)

	return rr
}

func serveHTTPMux(t *testing.T, handler Handler, endpoint string, reqBytes []byte, // nolint: unparam
	urlVars map[string]string) *httptest.ResponseRecorder {
	r, err := http.NewRequest(handler.Method(), endpoint, bytes.NewBuffer(reqBytes))
	require.NoError(t, err)

	rr := httptest.NewRecorder()

	req1 := mux.SetURLVars(r, urlVars)

	handler.Handle().ServeHTTP(rr, req1)

	return rr
}

const vc = `{
	   "@context":[
		  "https://www.w3.org/2018/credentials/v1",
		  "https://www.w3.org/2018/credentials/examples/v1"
	   ],
	   "id":"http://example.edu/credentials/1872",
	   "type":[
		  "VerifiableCredential",
		  "DIDConnectCredential"
	   ],
	   "credentialSubject":{
		  "id": "e9e0f944-7b74-4298-9f3e-00ca609d6266",
		  "inviteeDID": "did:example:7b744298e9e0f",
		  "inviterDID": "agc",
		  "inviterLabel": "user-agent"
	   },
	   "issuer":"did:example:76e12ec712ebc6f1c221ebfeb1f",
	   "issuanceDate":"2010-01-01T19:23:24Z"
	}`

func getTestVP(t *testing.T) []byte {
	vc, err := verifiable.ParseCredential([]byte(vc))
	require.NoError(t, err)

	vp, err := vc.Presentation()
	require.NoError(t, err)

	vpJSON, err := vp.MarshalJSON()
	require.NoError(t, err)

	return vpJSON
}
