// Copyright 2015 go-swagger maintainers
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package generator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"kcl-lang.io/kcl-openapi/pkg/kube_resource/generator/assets/static"
)

const (
	k8sSpecFile         = "api_spec/k8s/k8s.json"
	objectMetaSchemaRef = "k8s.json#/definitions/k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"
)

var (
	swaggerPartialObjectMetadataDescriptions = metav1beta1.PartialObjectMetadata{}.SwaggerDoc()
	swaggerTypeMetadataDescriptions          = v1.TypeMeta{}.SwaggerDoc()
	k8sFile                                  = static.Files[k8sSpecFile]
)

func init() {
	install.Install(scheme.Scheme)
}

func GetSpec(opts *GenOpts) (string, error) {
	// read crd content from file
	path, err := filepath.Abs(opts.Spec)
	if err != nil {
		return "", fmt.Errorf("could not locate spec: %s, err: %s", opts.Spec, err)
	}
	crdContent, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not load spec: %s, err: %s", opts.Spec, err)
	}
	// generate openapi spec from crd
	swagger, err := generate(string(crdContent))
	if err != nil {
		return "", fmt.Errorf("could not generate swagger spec: %s, err: %s", opts.Spec, err)
	}
	// write openapi spec to tmp file, along with the referenced k8s.json
	swaggerContent, err := json.MarshalIndent(swagger, "", "")
	if err != nil {
		return "", fmt.Errorf("could not validate swagger spec: %s, err: %s", opts.Spec, err)
	}
	tmpSpecDir := os.TempDir()
	tmpFile, err := os.CreateTemp(tmpSpecDir, "kcl-swagger-")
	if err != nil {
		return "", fmt.Errorf("could not validate swagger spec: %s, err: %s", opts.Spec, err)
	}
	// copy k8s.json to tmpDir
	if err := os.WriteFile(filepath.Join(tmpSpecDir, "k8s.json"), []byte(k8sFile), 0644); err != nil {
		return "", fmt.Errorf("could not generate swagger spec file: %s, err: %s", opts.Spec, err)
	}
	if _, err := tmpFile.Write(swaggerContent); err != nil {
		return "", fmt.Errorf("could not generate swagger spec file: %s, err: %s", opts.Spec, err)
	}
	// return the tmp openapi spec file path
	return tmpFile.Name(), nil
}

// GetSpecs retrieves specifications from the given GenOpts and returns a list of temporary file paths for the generated OpenAPI specs.
// It returns an error if there is any issue in fetching and generating the specs.
// Parameters:
// - opts: a GenOpts struct that contains the options and parameters required for generating the specs
// Returns:
// - []string: a list of temporary file paths for the generated OpenAPI specs
// - error: an error message if any error occurs.
func GetSpecs(opts *GenOpts) ([]string, error) {
	var result []string
	// read crd content from file
	path, err := filepath.Abs(opts.Spec)
	if err != nil {
		return result, fmt.Errorf("could not locate spec: %s, err: %s", opts.Spec, err)
	}
	crdContent, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("could not load spec: %s, err: %s", opts.Spec, err)
	}
	contents, err := splitDocuments(string(crdContent))
	if err != nil {
		return result, fmt.Errorf("could not load spec: %s, err: %s", opts.Spec, err)
	}
	for _, content := range contents {
		// generate openapi spec from crd
		swagger, err := generate(content)
		if err != nil {
			return result, fmt.Errorf("could not generate swagger spec: %s, err: %s", opts.Spec, err)
		}
		// write openapi spec to tmp file, along with the referenced k8s.json
		swaggerContent, err := json.MarshalIndent(swagger, "", "")
		if err != nil {
			return result, fmt.Errorf("could not validate swagger spec: %s, err: %s", opts.Spec, err)
		}
		tmpSpecDir := os.TempDir()
		tmpFile, err := os.CreateTemp(tmpSpecDir, "kcl-swagger-")
		if err != nil {
			return result, fmt.Errorf("could not validate swagger spec: %s, err: %s", opts.Spec, err)
		}
		// copy k8s.json to tmpDir
		if err := os.WriteFile(filepath.Join(tmpSpecDir, "k8s.json"), []byte(k8sFile), 0644); err != nil {
			return result, fmt.Errorf("could not generate swagger spec file: %s, err: %s", opts.Spec, err)
		}
		if _, err := tmpFile.Write(swaggerContent); err != nil {
			return result, fmt.Errorf("could not generate swagger spec file: %s, err: %s", opts.Spec, err)
		}
		// Append the tmp openapi spec file path
		result = append(result, tmpFile.Name())
	}
	return result, nil
}

// splitDocuments returns a slice of all documents contained in a YAML string. Multiple documents can be divided by the
// YAML document separator (---). It allows for white space and comments to be after the separator on the same line,
// but will return an error if anything else is on the line.
func splitDocuments(s string) ([]string, error) {
	docs := make([]string, 0)
	if len(s) > 0 {
		// The YAML document separator is any line that starts with ---
		yamlSeparatorRegexp := regexp.MustCompile(`\n---.*\n`)

		// Find all separators, check them for invalid content, and append each document to docs
		separatorLocations := yamlSeparatorRegexp.FindAllStringIndex(s, -1)
		prev := 0
		for i := range separatorLocations {
			loc := separatorLocations[i]
			separator := s[loc[0]:loc[1]]
			// If the next non-whitespace character on the line following the separator is not a comment, return an error
			trimmedContentAfterSeparator := strings.TrimSpace(separator[4:])
			if len(trimmedContentAfterSeparator) > 0 && trimmedContentAfterSeparator[0] != '#' {
				return nil, fmt.Errorf("invalid document separator: %s", strings.TrimSpace(separator))
			}

			docs = append(docs, s[prev:loc[0]])
			prev = loc[1]
		}
		docs = append(docs, s[prev:])
	}
	return docs, nil
}

// generate swagger model based on crd
func generate(crdYaml string) (*spec.Swagger, error) {
	crdObj, _, err := scheme.Codecs.UniversalDeserializer().
		Decode([]byte(crdYaml), nil, nil)
	if err != nil {
		return nil, err
	}
	crd, err := crdObj2CrdInternal(crdObj)
	if err != nil {
		return nil, err
	}
	return buildSwagger(crd)
}

func crdObj2CrdInternal(crdObj runtime.Object) (*apiextensions.CustomResourceDefinition, error) {
	var crd *apiextensions.CustomResourceDefinition
	switch crdObj.(type) {
	case *v1beta1.CustomResourceDefinition:
		// on v1beta1: v1beta1 support both validation & versions.
		// If the validation field is present, this validation schema is used to validate all versions
		// If the validation filed is not present, use the first item in the versions field
		// If neither of the validation & versions fields is present, that means the crd is lack of schema validation description and should raise err.
		crd = &apiextensions.CustomResourceDefinition{}
		v1beta1.Convert_v1beta1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(crdObj.(*v1beta1.CustomResourceDefinition), crd, nil)
		if crd.Spec.Validation == nil {
			if len(crd.Spec.Versions) >= 1 && crd.Spec.Versions[0].Schema != nil {
				crd.Spec.Validation = crd.Spec.Versions[0].Schema
			}
		}
	case *apiextv1.CustomResourceDefinition:
		// on v1
		crd = &apiextensions.CustomResourceDefinition{}
		apiextv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(crdObj.(*apiextv1.CustomResourceDefinition), crd, nil)
	case *apiextensions.CustomResourceDefinition:
		crd = crdObj.(*apiextensions.CustomResourceDefinition)
	default:
		return nil, errors.New(fmt.Sprintf("unknown crd object type %v", crdObj.GetObjectKind()))
	}

	if !CRDContainsValidation(crd) {
		return nil, errors.New("no openapi schema found in the crd file. Please check following fields: \nspec.Versions.<n>.Schema, spec.Versions.<n>.Schema.OpenAPIV3Schema, spec.Validation.OpenAPIV3Schema, spec.Versions.0.Schema")
	}
	return crd, nil
}

func CRDContainsValidation(crd *apiextensions.CustomResourceDefinition) bool {
	if crd.Spec.Validation != nil && crd.Spec.Validation.OpenAPIV3Schema != nil {
		return true
	}
	for _, version := range crd.Spec.Versions {
		if version.Schema != nil && version.Schema.OpenAPIV3Schema != nil {
			return true
		}
	}
	return false
}

func buildSwagger(crd *apiextensions.CustomResourceDefinition) (*spec.Swagger, error) {
	var schemas spec.Definitions = map[string]spec.Schema{}
	group, kind := crd.Spec.Group, crd.Spec.Names.Kind
	if crd.Spec.Validation != nil && crd.Spec.Validation.OpenAPIV3Schema != nil {
		var schema spec.Schema
		err := validation.ConvertJSONSchemaProps(crd.Spec.Validation.OpenAPIV3Schema, &schema)
		if err != nil {
			return nil, err
		}
		var version string
		if len(crd.Spec.Versions) >= 0 {
			version = crd.Spec.Versions[0].Name
		} else {
			version = crd.Spec.Version
		}
		setKubeNative(&schema, group, version, kind)
		name := fmt.Sprintf("%s.%s.%s", group, version, kind)
		schemas[name] = schema
	} else if len(crd.Spec.Versions) > 0 {
		for _, version := range crd.Spec.Versions {
			if version.Schema != nil && version.Schema.OpenAPIV3Schema != nil {
				var schema spec.Schema
				err := validation.ConvertJSONSchemaProps(version.Schema.OpenAPIV3Schema, &schema)
				if err != nil {
					return nil, err
				}
				version := version.Name
				setKubeNative(&schema, group, version, kind)
				name := fmt.Sprintf("%s.%s.%s", group, version, kind)
				schemas[name] = schema
			}
		}
	}

	// todo: set extensions, include kcl-type and user-defined extensions
	return &spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger:     "2.0", // todo: support swagger 3.0
			Definitions: schemas,
			Paths:       &spec.Paths{},
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:   "Kubernetes CRD Swagger",
					Version: "v0.1.0",
				},
			},
		},
	}, nil
}

func setKubeNative(schema *spec.Schema, group string, version string, kind string) {
	// set kube kind, version, group
	apiVersionSchema := spec.Schema{}
	apiVersionSchema.ReadOnly = true
	apiVersionSchema.Typed("string", "")
	apiVersionSchema.WithDefault(fmt.Sprintf("%s/%s", group, version))
	apiVersionSchema.WithDescription(swaggerTypeMetadataDescriptions["apiVersion"])
	kindSchema := spec.Schema{}
	kindSchema.ReadOnly = true
	kindSchema.Typed("string", "")
	kindSchema.WithDefault(kind)
	kindSchema.WithDescription(swaggerTypeMetadataDescriptions["kind"])
	schema.SetProperty("apiVersion", apiVersionSchema)
	schema.SetProperty("kind", kindSchema)
	schema.SetProperty("metadata", *spec.RefSchema(objectMetaSchemaRef).
		WithDescription(swaggerPartialObjectMetadataDescriptions["metadata"]))
	// todo: update more k8s refs to kcl format
}
