package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/kube-openapi/pkg/schemaconv"
	"k8s.io/kube-openapi/pkg/util/proto"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/structured-merge-diff/v4/fieldpath"
	mergeDiffSchema "sigs.k8s.io/structured-merge-diff/v4/schema"
	"sigs.k8s.io/structured-merge-diff/v4/typed"
)

func TestIssue(t *testing.T) {
	ctx := context.Background()

	// Create new creator instance.
	r, err := New(ctx, cfg)
	if err != nil {
		panic(err)
	}

	// Service GVK.
	gvk := schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Service",
	}

	// Create parseable type for service GVK.
	objectType := r.ParseableType(ctx, gvk)
	if objectType == nil {
		panic("failed to fetch the objectType: " + gvk.String())
	}
	if !objectType.IsValid() {
		panic("parseable type for GVK %v not valid" + gvk.String())
	}
	// logrus.Infof("objectType: %s", JsonObjectToString(objectType))

	// Service yaml for simulation:
	// The yaml was 'kubectl apply'ed followed by editting 'ports.nodeport'
	// field with 'kubectl edit'.
	// The thing to note is that there are thus 2 field managers:
	// - 'kubectl-client-side-apply': Owns everything.
	// - 'kubectl-edit': Shares ownership of the field 'ports.nodeport'.
	object := jsonToUnstructured(`{"apiVersion":"v1","kind":"Service","metadata":{"annotations":{},"managedFields":[{"apiVersion":"v1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:kubectl.kubernetes.io/last-applied-configuration":{}}},"f:spec":{"f:externalTrafficPolicy":{},"f:internalTrafficPolicy":{},"f:ports":{".":{},"k:{\"port\":80,\"protocol\":\"TCP\"}":{".":{},"f:name":{},"f:port":{},"f:protocol":{},"f:targetPort":{}}},"f:selector":{},"f:sessionAffinity":{},"f:type":{}}},"manager":"kubectl-client-side-apply","operation":"Update","time":"2023-12-21T05:29:51Z"},{"apiVersion":"v1","fieldsType":"FieldsV1","fieldsV1":{"f:spec":{"f:ports":{"k:{\"port\":80,\"protocol\":\"TCP\"}":{"f:nodePort":{}}}}},"manager":"kubectl-edit","operation":"Update","time":"2023-12-21T05:59:59Z"}],"name":"clear-nginx-service"},"spec":{"clusterIP":"172.19.41.134","clusterIPs":["172.19.41.134"],"externalTrafficPolicy":"Cluster","internalTrafficPolicy":"Cluster","ipFamilies":["IPv4"],"ipFamilyPolicy":"SingleStack","ports":[{"name":"http","nodePort":30001,"port":80,"protocol":"TCP","targetPort":80}],"selector":{"app":"clear-nginx"},"sessionAffinity":"None","type":"NodePort"}}`)

	objManagedFields := object.GetManagedFields()
	origObj, err := objectType.FromUnstructured(object.Object)

	for _, managedField := range objManagedFields {
		if managedField.Manager == "kubectl-client-side-apply" {
			// Simulation: The above object has gone through this already. Thus, continue.
			continue
		}
		// Reaching here means: The managedField here is kubectl-edit.
		logrus.Infof("managedField: %v", managedField.Manager)
		field := &managedField
		fieldset := &fieldpath.Set{}
		err := fieldset.FromJSON(bytes.NewReader(field.FieldsV1.Raw))
		if err != nil {
			panic(err)
		}

		logrus.Info("original object before extracting fields", "origObject", origObj.AsValue())
		extractedObj := origObj.ExtractItems(fieldset.Leaves())
		// This is how the extractedObj looks like:
		// Carefully note that the required fields in 'ports' for 'merge' operation, ie port & protocol, are not there. And they should rightfully not be there.
		//extractedObj, err = objectType.FromUnstructured(jsonToInterface(`{"spec":{"ports":[{"nodePort":30001}]}}`))

		// If the extracted object looks like this, there won't be any error.
		// extractedObj, err = objectType.FromUnstructured(jsonToInterface(`{
		//    "spec": {
		//        "ports": [
		//            {
		//                "nodePort": 30001,
		//                "port": 81,
		//                "protocol": "TCP"
		//            }
		//        ]
		//    }
		//}`))
		// if err != nil {
		// 	panic(err)
		// }
		logrus.Info("extracted items before merge", "extractedObj", JsonObjectToString(extractedObj.AsValue()))

		// Simulation: This is the new object which is created after having
		// merged with the first field manager 'kubectl-client-side-apply'.
		// We now want to merge the extracted fields from the second field
		// manager 'kubectl-edit'.
		newObj, err := objectType.FromUnstructured(jsonToInterface(`{"metadata":{"annotations":null},"spec":{"externalTrafficPolicy":"Cluster","internalTrafficPolicy":"Cluster","ports":[{"name":"http","port":80,"protocol":"TCP","targetPort":80}],"selector":{"app":"clear-nginx"},"sessionAffinity":"None","type":"NodePort"}}`))
		if err != nil {
			panic(err)
		}
		o, err := newObj.Merge(extractedObj)
		if err != nil {
			panic("failed to merge objects: " + err.Error())
		}
		// This returns error:
		// panic: failed to merge objects: .spec.ports: element 0: associative list with keys has an element that omits key field "port" (and doesn't have default value)
		logrus.Infof("%v", JsonObjectToString(o))
	}
}

type Creator struct {
	restConfig       *rest.Config
	gvkToTypeNameMap map[schema.GroupVersionKind]string // Map from gvk to type name.
	schema           *mergeDiffSchema.Schema
}

func New(ctx context.Context, restConfig *rest.Config) (*Creator, error) {
	log := log.FromContext(ctx)

	dc := discovery.NewDiscoveryClientForConfigOrDie(restConfig)
	doc, err := dc.OpenAPISchema()
	if err != nil {
		return nil, err
	}
	models, err := proto.NewOpenAPIData(doc)
	if err != nil {
		return nil, err
	}
	typeSchema, err := schemaconv.ToSchemaWithPreserveUnknownFields(models, false)
	if err != nil {
		return nil, fmt.Errorf("failed to convert models to schema: %v", err)
	}

	creator := &Creator{
		restConfig:       restConfig,
		gvkToTypeNameMap: make(map[schema.GroupVersionKind]string),
		schema:           typeSchema,
	}

	// Construct map of GVK to type name. Parseable types expect type name together with schema.
	for _, modelName := range models.ListModels() {
		model := models.LookupModel(modelName)
		if model == nil {
			return nil, fmt.Errorf("ListModels returns a model that can't be looked-up for: %v", modelName)
		}
		gvkList := parseGroupVersionKind(model)
		for _, gvk := range gvkList {
			if len(gvk.Kind) > 0 {
				if existingModelName, ok := creator.gvkToTypeNameMap[gvk]; ok {
					log.Info("duplicate GVK entry in OpenAPI schema", "gvk", gvk,
						"modelName", modelName, "existingModelName", existingModelName)
				}
				creator.gvkToTypeNameMap[gvk] = modelName
			}
		}
	}

	return creator, nil
}

// ParseableType constructs structured-merge-diff type from GVK.
func (r *Creator) ParseableType(ctx context.Context, gvk schema.GroupVersionKind) *typed.ParseableType {
	log := log.FromContext(ctx)

	typeName, ok := r.gvkToTypeNameMap[gvk]
	if !ok {
		return nil
	}
	log.V(1).Info("Model for GVK", "gvk", gvk, "typeName", typeName)
	return &typed.ParseableType{
		Schema:  r.schema,
		TypeRef: mergeDiffSchema.TypeRef{NamedType: &typeName},
	}
}

func parseGroupVersionKind(s proto.Schema) []schema.GroupVersionKind {
	const groupVersionKindExtensionKey = "x-kubernetes-group-version-kind"
	extensions := s.GetExtensions()

	gvkListResult := []schema.GroupVersionKind{}

	// Get the extensions
	gvkExtension, ok := extensions[groupVersionKindExtensionKey]
	if !ok {
		return []schema.GroupVersionKind{}
	}

	// gvk extension must be a list of at least 1 element.
	gvkList, ok := gvkExtension.([]interface{})
	if !ok {
		return []schema.GroupVersionKind{}
	}

	for _, gvk := range gvkList {
		// gvk extension list must be a map with group, version, and
		// kind fields
		gvkMap, ok := gvk.(map[interface{}]interface{})
		if !ok {
			continue
		}
		group, ok := gvkMap["group"].(string)
		if !ok {
			continue
		}
		version, ok := gvkMap["version"].(string)
		if !ok {
			continue
		}
		kind, ok := gvkMap["kind"].(string)
		if !ok {
			continue
		}

		gvkListResult = append(gvkListResult, schema.GroupVersionKind{
			Group:   group,
			Version: version,
			Kind:    kind,
		})
	}

	return gvkListResult
}

func jsonToInterface(j string) map[string]interface{} {
	ret := map[string]interface{}{}
	err := json.Unmarshal([]byte(j), &ret)
	if err != nil {
		panic(err)
	}
	return ret
}

func jsonToUnstructured(j string) *unstructured.Unstructured {
	ret := &unstructured.Unstructured{}
	err := json.Unmarshal([]byte(j), &ret.Object)
	if err != nil {
		panic(err)
	}
	return ret
}

func JsonObjectToString(j interface{}) string {
	b, err := json.Marshal(j)
	if err != nil {
		panic(err)
	}
	return string(b)
}
