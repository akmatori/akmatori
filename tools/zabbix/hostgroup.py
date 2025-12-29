"""
Host Group Management Tools for Zabbix

Example:
    from lib.zabbix_py.hostgroup import hostgroup_get, hostgroup_create

    # Get all host groups
    groups = hostgroup_get()

    # Create a new host group
    result = hostgroup_create({'name': 'Web Servers'})
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria
from .utils import zabbix_request


class HostgroupGetParams(TypedDict, total=False):
    """Parameters for hostgroup.get API method"""
    groupids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria


class HostgroupCreateParams(TypedDict):
    """Parameters for hostgroup.create API method"""
    name: str


class HostgroupUpdateParams(TypedDict):
    """Parameters for hostgroup.update API method"""
    groupid: str
    name: str


def hostgroup_get(params: Optional[HostgroupGetParams] = None) -> str:
    """
    Get host groups from Zabbix

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of host groups
    """
    if params is None:
        params = {}
    return zabbix_request('hostgroup.get', params)


def hostgroup_create(params: HostgroupCreateParams) -> str:
    """
    Create a new host group in Zabbix

    Args:
        params: Host group creation parameters

    Returns:
        JSON string containing creation result
    """
    return zabbix_request('hostgroup.create', params)


def hostgroup_update(params: HostgroupUpdateParams) -> str:
    """
    Update an existing host group in Zabbix

    Args:
        params: Host group update parameters

    Returns:
        JSON string containing update result
    """
    return zabbix_request('hostgroup.update', params)


def hostgroup_delete(groupids: List[str]) -> str:
    """
    Delete host groups from Zabbix

    Args:
        groupids: List of group IDs to delete

    Returns:
        JSON string containing deletion result
    """
    return zabbix_request('hostgroup.delete', {'groupids': groupids})
