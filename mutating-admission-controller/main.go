package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang/glog"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	port = "8080"
)

var (
	tlscert, tlskey string
)

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

type admitFunc func(v1.AdmissionReview) *v1.AdmissionResponse

func toAdmissionResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func serveMutateServiceAccount(w http.ResponseWriter, r *http.Request) {
	serve(w, r, mutateServiceAccount)
}

func serve(w http.ResponseWriter, r *http.Request, admit admitFunc) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		glog.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	// The AdmissionReview that was sent to the webhook
	requestedAdmissionReview := v1.AdmissionReview{}

	// The AdmissionReview that will be returned
	responseAdmissionReview := v1.AdmissionReview{}

	deserializer := scheme.Codecs.UniversalDeserializer()

	if _, _, err := deserializer.Decode(body, nil, &requestedAdmissionReview); err != nil {
		glog.Error(err)
		responseAdmissionReview.Response = toAdmissionResponse(err)
	} else {
		responseAdmissionReview.Response = admit(requestedAdmissionReview)
	}

	responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
	responseAdmissionReview.APIVersion = "admission.k8s.io/v1"
	responseAdmissionReview.Kind = "AdmissionReview"

	respBytes, err := json.Marshal(responseAdmissionReview)
	if err != nil {
		glog.Error(err)
	}
	if _, err := w.Write(respBytes); err != nil {
		glog.Error(err)
	}
}

func mutateServiceAccount(ar v1.AdmissionReview) *v1.AdmissionResponse {
	req := ar.Request

	glog.Infof("AdmissionReview for Kind=%v, Namespace=%v Name=%v UID=%v patchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, req.UID, req.Operation, req.UserInfo)

	if req.Kind.Kind == "ServiceAccount" && req.Name == "default" {
		reviewResponse := v1.AdmissionResponse{}
		reviewResponse.Allowed = true

		patch := []patchOperation{
			{
				Op:    "add",
				Path:  "/automountServiceAccountToken",
				Value: false,
			},
		}

		patchFinal, _ := json.Marshal(patch)

		reviewResponse.Patch = []byte(patchFinal)
		pt := v1.PatchTypeJSONPatch
		reviewResponse.PatchType = &pt

		glog.Infof("AdmissionResponse: patch=%v\n", string(patchFinal))
		return &reviewResponse
	}

	glog.Infof("Skipping mutation for Kind=%v Name=%v", req.Kind, req.Name)
	return &v1.AdmissionResponse{
		Allowed: true,
	}
}

func usage() {
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flag.Set("logtostderr", "true")
	flag.Set("stderrthreshold", "WARNING")
	flag.Set("v", "2")
	flag.StringVar(&tlscert, "tlsCertFile", "/etc/certs/cert.pem", "File containing the x509 Certificate for HTTPS.")
	flag.StringVar(&tlskey, "tlsKeyFile", "/etc/certs/key.pem", "File containing the x509 private key to --tlsCertFile.")
	flag.Parse()

	certs, err := tls.LoadX509KeyPair(tlscert, tlskey)
	if err != nil {
		glog.Errorf("Failed to load key pair: %v", err)
		return
	}

	server := &http.Server{
		Addr:      fmt.Sprintf(":%v", port),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certs}},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", serveMutateServiceAccount)
	server.Handler = mux

	go func() {
		if err := server.ListenAndServeTLS("", ""); err != nil {
			glog.Errorf("Failed to listen and serve webhook server: %v", err)
			return
		}
	}()

	glog.Infof("Server running in port: %s", port)

	// listening shutdown signal
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	glog.Info("Got shutdown signal, shutting down webhook server gracefully...")
	glog.Flush()
	server.Shutdown(context.Background())
}
