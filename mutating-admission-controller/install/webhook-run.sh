#!/bin/bash

set -e

usage() {
    cat <<EOF
Generate certificate suitable for use with an mutating webhook service.
This script uses k8s' CertificateSigningRequest API to a generate a
certificate signed by k8s CA suitable for use with mutating webhook
services. This requires permissions to create and approve CSR. See
https://kubernetes.io/docs/tasks/tls/managing-tls-in-a-cluster for
detailed explanation and additional instructions.
The server key/cert k8s CA cert are stored in a k8s secret.
usage: ${0} [OPTIONS]
The following flags are required.
       --service          Service name of webhook.
       --namespace        Namespace where webhook service and secret reside.
       --secret           Secret name for CA certificate and server certificate/key pair.
       --kubeconfigpath   Path for your kubeconfig.
EOF
    exit 1
}

while [[ $# -gt 0 ]]; do
    case ${1} in
        --service)
            service="$2"
            shift
            ;;
        --secret)
            secret="$2"
            shift
            ;;
        --namespace)
            namespace="$2"
            shift
            ;;
        --kubeconfigpath)
            kubeconfigpath="$2"
            shift
            ;;
        *)
            usage
            ;;
    esac
    shift
done

[ -z ${service} ] && service=mutating
[ -z ${secret} ] && secret=mutating
[ -z ${namespace} ] && namespace=default
[ -z ${kubeconfigpath} ] && kubeconfigpath=$KUBECONFIG

export KUBECONFIG=${kubeconfigpath}

echo "using KUBECONFIG: ${KUBECONFIG}"

if [ ! -x "$(command -v openssl)" ]; then
    echo "openssl not found"
    exit 1
fi

csrName=${service}.${namespace}
tmpdir=$(mktemp -d)
echo "creating certs in tmpdir ${tmpdir} "

cat <<EOF >> ${tmpdir}/csr.conf
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${service}
DNS.2 = ${service}.${namespace}
DNS.3 = ${service}.${namespace}.svc
EOF

openssl genrsa -out ${tmpdir}/server-key.pem 2048
openssl req -new -key ${tmpdir}/server-key.pem -subj "/CN=${service}.${namespace}.svc" -out ${tmpdir}/server.csr -config ${tmpdir}/csr.conf

# clean-up any previously created CSR for our service. Ignore errors if not present.
kubectl delete csr ${csrName} 2>/dev/null || true

# create  server cert/key CSR and  send to k8s API
cat <<EOF | kubectl create -f -
apiVersion: certificates.k8s.io/v1beta1
kind: CertificateSigningRequest
metadata:
  name: ${csrName}
spec:
  groups:
  - system:authenticated
  request: $(cat ${tmpdir}/server.csr | base64 | tr -d '\n')
  usages:
  - digital signature
  - key encipherment
  - server auth
EOF

# verify CSR has been created
while true; do
    kubectl get csr ${csrName}
    if [ "$?" -eq 0 ]; then
        break
    fi
done

# approve and fetch the signed certificate
kubectl certificate approve ${csrName}

# verify certificate has been signed
for x in $(seq 10); do
    serverCert=$(kubectl get csr ${csrName} -o jsonpath='{.status.certificate}')
    if [[ ${serverCert} != '' ]]; then
        break
    fi
    sleep 1
done
if [[ ${serverCert} == '' ]]; then
    echo "ERROR: After approving csr ${csrName}, the signed certificate did not appear on the resource. Giving up after 10 attempts." >&2
    exit 1
fi
echo ${serverCert} | openssl base64 -d -A -out ${tmpdir}/server-cert.pem

# create the secret with CA cert and server cert/key
kubectl create secret generic ${secret} \
        --from-file=key.pem=${tmpdir}/server-key.pem \
        --from-file=cert.pem=${tmpdir}/server-cert.pem \
        --dry-run=client -o yaml |
    kubectl -n ${namespace} apply -f -

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: mutating
  namespace: default
  labels:
    name: mutating
spec:
  ports:
  - name: mutating
    port: 443
    targetPort: 8080
  selector:
    name: mutating 
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mutating
  namespace: default
  labels:
    name: mutating
spec:
  replicas: 1
  selector:
    matchLabels:
      name: mutating
  template:
    metadata:
      name: mutating 
      labels:
        name: mutating
    spec:
      containers:
        - name: mutating
          image: kinvolk/mutating-admission-controller
          imagePullPolicy: Always
          args:
            - -v=4
            - 2>&1
          resources:
            limits:
              memory: 50Mi
              cpu: 300m
            requests:
              memory: 00Mi
              cpu: 300m
          volumeMounts:
            - name: webhook-certs
              mountPath: /etc/certs
              readOnly: true
          securityContext:
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            runAsUser: 65534
            runAsGroup: 65534
      volumes:
        - name: webhook-certs
          secret:
            secretName: mutating
EOF

while [[ $(kubectl get pods -l name=mutating -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]; do echo "waiting for pod" && sleep 1; done

CURRENT_CLUSTER=$(kubectl config view --raw --flatten -o json | jq -r '.contexts[] | select(.name=="'$(kubectl config current-context)'") | .context.cluster')

CA_BUNDLE=$(kubectl config view --raw --flatten -o json | jq -r '.clusters[] | select(.name == "'$CURRENT_CLUSTER'") | .cluster."certificate-authority-data"')


cat <<EOF | kubectl apply -f -
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: mutating
  labels:
    name: mutating
webhooks:
  - name: mutating.kinvolk.io
    clientConfig:
      caBundle: ${CA_BUNDLE}
      service:
        name: mutating
        namespace: default
        path: "/mutate"
    rules:
      - operations: ["CREATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["*"]
    sideEffects: None
    admissionReviewVersions: ["v1", "v1beta1"]
EOF

echo "Manifest file created successfully"
