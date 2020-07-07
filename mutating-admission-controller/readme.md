## Mutating admission controller

Follow the below commands

```bash
# create all kubernetes resources
make install-webhook

# Open a new termianl and run logs
kubectl logs deploy/mutating -f

# In a new terminal create a namespace
kubectl create ns testing
```
