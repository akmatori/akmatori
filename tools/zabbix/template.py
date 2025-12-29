"""
Template Management Tools for Zabbix

Example:
    from lib.zabbix_py.template import template_get, template_create

    # Get all templates
    templates = template_get()

    # Create a new template
    result = template_create({
        'host': 'Template OS Linux',
        'groups': [{'groupid': '1'}],
        'name': 'Linux Servers Template'
    })
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria, HostGroup
from .utils import zabbix_request


class TemplateGetParams(TypedDict, total=False):
    """Parameters for template.get API method"""
    templateids: List[str]
    groupids: List[str]
    hostids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria


class TemplateCreateParams(TypedDict, total=False):
    """Parameters for template.create API method"""
    host: str
    groups: List[HostGroup]
    name: str
    description: str


class TemplateUpdateParams(TypedDict, total=False):
    """Parameters for template.update API method"""
    templateid: str
    host: str
    name: str
    description: str


def template_get(params: Optional[TemplateGetParams] = None) -> str:
    """
    Get templates from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of templates
    """
    if params is None:
        params = {}
    return zabbix_request('template.get', params)


def template_create(params: TemplateCreateParams) -> str:
    """
    Create a new template in Zabbix

    Args:
        params: Template creation parameters

    Returns:
        JSON string containing creation result
    """
    return zabbix_request('template.create', params)


def template_update(params: TemplateUpdateParams) -> str:
    """
    Update an existing template in Zabbix

    Args:
        params: Template update parameters

    Returns:
        JSON string containing update result
    """
    return zabbix_request('template.update', params)


def template_delete(templateids: List[str]) -> str:
    """
    Delete templates from Zabbix

    Args:
        templateids: List of template IDs to delete

    Returns:
        JSON string containing deletion result
    """
    return zabbix_request('template.delete', {'templateids': templateids})
