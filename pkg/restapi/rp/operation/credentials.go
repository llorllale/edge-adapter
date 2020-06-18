/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package operation

import (
	"fmt"

	"github.com/hyperledger/aries-framework-go/pkg/doc/verifiable"
	"github.com/pkg/errors"

	"github.com/trustbloc/edge-adapter/pkg/internal/common/adapterutil"
	"github.com/trustbloc/edge-adapter/pkg/vc"
	"github.com/trustbloc/edge-adapter/pkg/vc/rp"
)

var errMalformedCredential = errors.New("malformed credential")

//nolint:unparam
func getCustomCredentials(vpBytes []byte) (*rp.DIDDocumentCredential, *vc.UserConsentCredential, error) {
	creds, err := parseCredentials(vpBytes)
	if err != nil {
		return nil, nil, err
	}

	return parseCustomCredentials(creds)
}

func parseCredentials(vpBytes []byte) ([2]*verifiable.Credential, error) {
	const numCredentialsRequired = 2

	vp, err := verifiable.ParsePresentation(vpBytes)
	if err != nil {
		return [2]*verifiable.Credential{},
			errors.Wrapf(errMalformedCredential, fmt.Sprintf("error parsing a verifiable presentation : %s", err))
	}

	rawCreds, err := vp.MarshalledCredentials()
	if err != nil {
		return [2]*verifiable.Credential{}, fmt.Errorf("failed to marshal credentials from vp : %w", err)
	}

	if len(rawCreds) != numCredentialsRequired {
		return [2]*verifiable.Credential{},
			errors.Wrapf(
				errMalformedCredential,
				fmt.Sprintf(
					"received %d but expecting 2 verifiable credentials in the verifiable presentation",
					len(rawCreds)))
	}

	var allCreds [2]*verifiable.Credential

	for i, raw := range rawCreds {
		cred, err := verifiable.ParseCredential(raw)
		if err != nil {
			return [2]*verifiable.Credential{},
				fmt.Errorf("failed to parse raw credential %s : %w", string(raw), err)
		}

		allCreds[i] = cred
	}

	return allCreds, nil
}

func parseCustomCredentials(
	creds [2]*verifiable.Credential) (*rp.DIDDocumentCredential, *vc.UserConsentCredential, error) {
	var (
		issuerDIDVC *rp.DIDDocumentCredential
		consentVC   *vc.UserConsentCredential
	)

	for _, cred := range creds {
		if adapterutil.StringsContains(rp.DIDDocumentCredentialType, cred.Types) {
			if issuerDIDVC != nil {
				return nil, nil, errors.Wrapf(errMalformedCredential, "duplicate did doc credential")
			}

			issuerDIDVC = &rp.DIDDocumentCredential{}

			err := adapterutil.DecodeIntoCustomCredential(cred, issuerDIDVC)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to decode did doc vc : %w", err)
			}

			continue
		}

		if adapterutil.StringsContains(vc.UserConsentCredentialType, cred.Types) {
			if consentVC != nil {
				return nil, nil, errors.Wrapf(errMalformedCredential, "duplicate user consent credential")
			}

			consentVC = &vc.UserConsentCredential{}

			err := adapterutil.DecodeIntoCustomCredential(cred, consentVC)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to decode user consent credential : %w", err)
			}

			continue
		}

		return nil, nil, errors.Wrapf(errMalformedCredential, "unrecognized vc types %+v", cred.Types)
	}

	return issuerDIDVC, consentVC, nil
}
