package security

import (
	"errors"
	"net/http"

	"github.com/portainer/agent"
	"github.com/portainer/portainer/pkg/fips"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
)

type NotaryService struct {
	signatureService      agent.DigitalSignatureService
	signatureVerification bool
}

func NewNotaryService(signatureService agent.DigitalSignatureService, signatureVerification bool) *NotaryService {
	return newNotaryService(signatureService, signatureVerification, fips.FIPSMode())
}

func newNotaryService(signatureService agent.DigitalSignatureService, signatureVerification, fipsMode bool) *NotaryService {
	return &NotaryService{
		signatureVerification: signatureVerification && !fipsMode,
		signatureService:      signatureService,
	}
}

func (service *NotaryService) DigitalSignatureVerification(next http.Handler) http.Handler {
	return httperror.LoggerHandler(func(rw http.ResponseWriter, r *http.Request) *httperror.HandlerError {
		if service.signatureVerification {
			publicKeyHeaderValue := r.Header.Get(agent.HTTPPublicKeyHeaderName)
			signatureHeaderValue := r.Header.Get(agent.HTTPSignatureHeaderName)

			if publicKeyHeaderValue == "" || signatureHeaderValue == "" {
				return httperror.Forbidden("Missing request signature headers", errors.New("Unauthorized"))
			}

			valid, err := service.signatureService.VerifySignature(signatureHeaderValue, publicKeyHeaderValue)
			if err != nil {
				return httperror.Forbidden("Invalid request signature", err)
			} else if !valid {
				return httperror.Forbidden("Invalid request signature", errors.New("Unauthorized"))
			}
		}

		next.ServeHTTP(rw, r)

		return nil
	})
}
