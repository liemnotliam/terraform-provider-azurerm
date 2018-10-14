package azurerm

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2016-12-01/policy"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/structure"
	"github.com/hashicorp/terraform/helper/validation"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func resourceArmManagementGroupPolicyDefinition() *schema.Resource {
	return &schema.Resource{
		Create: resourceArmManagementGroupPolicyDefinitionCreateUpdate,
		Update: resourceArmManagementGroupPolicyDefinitionCreateUpdate,
		Read:   resourceArmManagementGroupPolicyDefinitionRead,
		Delete: resourceArmManagementGroupPolicyDefinitionDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"policy_type": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
				ValidateFunc: validation.StringInSlice([]string{
					string(policy.TypeBuiltIn),
					string(policy.TypeCustom),
					string(policy.TypeNotSpecified),
				}, true)},

			"mode": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
				ValidateFunc: validation.StringInSlice([]string{
					string(policy.All),
					string(policy.Indexed),
					string(policy.NotSpecified),
				}, true),
			},

			"display_name": {
				Type:     schema.TypeString,
				Required: true,
			},

			"description": {
				Type:     schema.TypeString,
				Optional: true,
			},

			"policy_rule": {
				Type:             schema.TypeString,
				Optional:         true,
				ValidateFunc:     validation.ValidateJsonString,
				DiffSuppressFunc: structure.SuppressJsonDiff,
			},

			"metadata": {
				Type:             schema.TypeString,
				Optional:         true,
				ValidateFunc:     validation.ValidateJsonString,
				DiffSuppressFunc: structure.SuppressJsonDiff,
			},

			"parameters": {
				Type:             schema.TypeString,
				Optional:         true,
				ValidateFunc:     validation.ValidateJsonString,
				DiffSuppressFunc: structure.SuppressJsonDiff,
			},

			"management_group_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
		},
	}
}

func resourceArmManagementGroupPolicyDefinitionCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).policyDefinitionsClient
	ctx := meta.(*ArmClient).StopContext

	name := d.Get("name").(string)
	policyType := d.Get("policy_type").(string)
	mode := d.Get("mode").(string)
	displayName := d.Get("display_name").(string)
	description := d.Get("description").(string)
	managementGroupID := d.Get("management_group_id").(string)

	properties := policy.DefinitionProperties{
		PolicyType:  policy.Type(policyType),
		Mode:        policy.Mode(mode),
		DisplayName: utils.String(displayName),
		Description: utils.String(description),
	}

	if policyRuleString := d.Get("policy_rule").(string); policyRuleString != "" {
		policyRule, err := structure.ExpandJsonFromString(policyRuleString)
		if err != nil {
			return fmt.Errorf("unable to parse policy_rule: %s", err)
		}
		properties.PolicyRule = &policyRule
	}

	if metaDataString := d.Get("metadata").(string); metaDataString != "" {
		metaData, err := structure.ExpandJsonFromString(metaDataString)
		if err != nil {
			return fmt.Errorf("unable to parse metadata: %s", err)
		}
		properties.Metadata = &metaData
	}

	if parametersString := d.Get("parameters").(string); parametersString != "" {
		parameters, err := structure.ExpandJsonFromString(parametersString)
		if err != nil {
			return fmt.Errorf("unable to parse parameters: %s", err)
		}
		properties.Parameters = &parameters
	}

	definition := policy.Definition{
		Name:                 utils.String(name),
		DefinitionProperties: &properties,
	}

	_, err := client.CreateOrUpdateAtManagementGroup(ctx, name, definition, managementGroupID)
	if err != nil {
		return fmt.Errorf("Error Creating/Updating Management Group Policy %q: %+v", name, err)
	}

	// Management Group Policy Definitions are eventually consistent; wait for them to stabilize
	log.Printf("[DEBUG] Waiting for Management Group Policy Definition %q to become available", name)
	stateConf := &resource.StateChangeConf{
		Pending:                   []string{"404"},
		Target:                    []string{"200"},
		Refresh:                   managementGroupPolicyDefinitionRefreshFunc(ctx, client, name, managementGroupID),
		Timeout:                   5 * time.Minute,
		MinTimeout:                10 * time.Second,
		ContinuousTargetOccurence: 10,
	}
	if _, err := stateConf.WaitForState(); err != nil {
		return fmt.Errorf("Error waiting for Management Group Policy Definition %q to become available: %s", name, err)
	}

	resp, err := client.GetAtManagementGroup(ctx, name, managementGroupID)
	if err != nil {
		return fmt.Errorf("Error retrieving Management Group Policy Definition %q: %+v", name, err)
	}

	d.SetId(*resp.ID)

	return resourceArmManagementGroupPolicyDefinitionRead(d, meta)
}

func resourceArmManagementGroupPolicyDefinitionRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).policyDefinitionsClient
	ctx := meta.(*ArmClient).StopContext

	id, err := parseManagementGroupPolicyDefinitionNameFromID(d.Id())
	if err != nil {
		return fmt.Errorf("Error parsing Management Group Policy Definition name from ID %s: %s", d.Id(), err)
	}

	resp, err := client.GetAtManagementGroup(ctx, id.Name, id.ManagementGroupID)
	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			log.Printf("[DEBUG] Error reading Management Group Policy Definition %q - removing from state", d.Id())
			d.SetId("")
			return nil
		}

		return fmt.Errorf("Error making read request on Management Group Policy Definition %+v", err)
	}

	d.Set("name", resp.Name)
	d.Set("management_group_id", id.ManagementGroupID)

	if props := resp.DefinitionProperties; props != nil {
		d.Set("policy_type", props.PolicyType)
		d.Set("mode", props.Mode)
		d.Set("display_name", props.DisplayName)
		d.Set("description", props.Description)

		if policyRule := props.PolicyRule; policyRule != nil {
			policyRuleVal := policyRule.(map[string]interface{})
			policyRuleStr, err := structure.FlattenJsonToString(policyRuleVal)
			if err != nil {
				return fmt.Errorf("unable to flatten JSON for `policy_rule`: %s", err)
			}

			d.Set("policy_rule", policyRuleStr)
		}

		if metadata := props.Metadata; metadata != nil {
			metadataVal := metadata.(map[string]interface{})
			metadataStr, err := structure.FlattenJsonToString(metadataVal)
			if err != nil {
				return fmt.Errorf("unable to flatten JSON for `metadata`: %s", err)
			}

			d.Set("metadata", metadataStr)
		}

		if parameters := props.Parameters; parameters != nil {
			paramsVal := props.Parameters.(map[string]interface{})
			parametersStr, err := structure.FlattenJsonToString(paramsVal)
			if err != nil {
				return fmt.Errorf("unable to flatten JSON for `parameters`: %s", err)
			}

			d.Set("parameters", parametersStr)
		}
	}

	return nil
}

func resourceArmManagementGroupPolicyDefinitionDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).policyDefinitionsClient
	ctx := meta.(*ArmClient).StopContext

	id, err := parseManagementGroupPolicyDefinitionNameFromID(d.Id())
	if err != nil {
		return fmt.Errorf("Error parsing Management Group Policy Definition name from ID %s: %s", d.Id(), err)
	}

	resp, err := client.DeleteAtManagementGroup(ctx, id.Name, id.ManagementGroupID)

	if err != nil {
		if utils.ResponseWasNotFound(resp) {
			return nil
		}

		return fmt.Errorf("Error deleting Policy Definition %q: %+v", id.Name, err)
	}

	return nil
}

type managementGroupPolicyResourceID struct {
	ManagementGroupID string
	Name              string
}

func parseManagementGroupPolicyDefinitionNameFromID(id string) (*managementGroupPolicyResourceID, error) {
	// /providers/Microsoft.Management/managementGroups/{mg-name}/providers/Microsoft.Authorization/policyDefinitions/{name}
	idObj := &managementGroupPolicyResourceID{}
	components := strings.Split(id, "/")

	if len(components) == 0 {
		return nil, fmt.Errorf("Azure Management Group Policy Definition Id is empty or not formatted correctly: %s", id)
	}

	if len(components) != 9 {
		return nil, fmt.Errorf("Azure Management Group Policy Definition Id should have 8 segments, got %d: '%s'", len(components)-1, id)
	}

	idObj.ManagementGroupID = strings.Join(components[0:5], "/")
	idObj.Name = components[len(components)-1]

	return idObj, nil
}

func managementGroupPolicyDefinitionRefreshFunc(ctx context.Context, client policy.DefinitionsClient, name string, managementGroupID string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		res, err := client.GetAtManagementGroup(ctx, name, managementGroupID)
		if err != nil {
			return nil, strconv.Itoa(res.StatusCode), fmt.Errorf("Error issuing read request in managementGroupPolicyDefinitionRefreshFunc for Management Group Policy Defintion %q: %s", name, err)
		}

		return res, strconv.Itoa(res.StatusCode), nil
	}
}
