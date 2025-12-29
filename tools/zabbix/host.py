"""
Host Management Tools for Zabbix

These functions provide access to Zabbix host management capabilities.
Import and use them directly in your Python code.

Example:
    from lib.zabbix_py.host import host_get, host_create

    # Get all hosts
    hosts = host_get()

    # Get hosts in specific group
    group_hosts = host_get({'groupids': ["1"]})
"""

from typing import TypedDict, Optional, List, Dict, Any
from .types import OutputFormat, SearchCriteria, FilterCriteria, HostGroup, Template, HostInterface
from .utils import zabbix_request


class HostGetParams(TypedDict, total=False):
    """Parameters for host.get API method"""
    hostids: List[str]
    groupids: List[str]
    templateids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria
    limit: int


class HostCreateParams(TypedDict, total=False):
    """Parameters for host.create API method"""
    host: str
    groups: List[HostGroup]
    interfaces: List[HostInterface]
    templates: List[Template]
    inventory_mode: int
    status: int


class HostUpdateParams(TypedDict, total=False):
    """Parameters for host.update API method"""
    hostid: str
    host: str
    name: str
    status: int


def host_get(params: Optional[HostGetParams] = None) -> str:
    """
    Get hosts from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of hosts

    Example:
        import json
        from lib.zabbix_py.host import host_get

        result = host_get({'output': 'extend', 'limit': 10})
        hosts = json.loads(result)
        for host in hosts:
            print(f"Host: {host['name']}")
    """
    if params is None:
        params = {}
    return zabbix_request('host.get', params)


def host_create(params: HostCreateParams) -> str:
    """
    Create a new host in Zabbix

    Args:
        params: Host creation parameters

    Returns:
        JSON string containing creation result

    Example:
        from lib.zabbix_py.host import host_create

        result = host_create({
            'host': 'server-01',
            'groups': [{'groupid': '1'}],
            'interfaces': [{
                'type': 1,
                'main': 1,
                'useip': 1,
                'ip': '192.168.1.100',
                'dns': '',
                'port': '10050'
            }]
        })
    """
    return zabbix_request('host.create', params)


def host_update(params: HostUpdateParams) -> str:
    """
    Update an existing host in Zabbix

    Args:
        params: Host update parameters

    Returns:
        JSON string containing update result

    Example:
        from lib.zabbix_py.host import host_update

        result = host_update({
            'hostid': '10001',
            'status': 0  # Enable host
        })
    """
    return zabbix_request('host.update', params)


def host_delete(hostids: List[str]) -> str:
    """
    Delete hosts from Zabbix

    Args:
        hostids: List of host IDs to delete

    Returns:
        JSON string containing deletion result

    Example:
        from lib.zabbix_py.host import host_delete

        result = host_delete(['10001', '10002'])
    """
    return zabbix_request('host.delete', hostids)
