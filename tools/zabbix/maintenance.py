"""
Maintenance Management Tools for Zabbix

Example:
    from lib.zabbix_py.maintenance import maintenance_get, maintenance_create

    # Get all maintenance periods
    maintenances = maintenance_get()

    # Create a new maintenance period
    result = maintenance_create({
        'name': 'Weekend Maintenance',
        'active_since': 1640995200,
        'active_till': 1641081600,
        'hostids': ['10001']
    })
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, TimePeriod
from .utils import zabbix_request


class MaintenanceGetParams(TypedDict, total=False):
    """Parameters for maintenance.get API method"""
    maintenanceids: List[str]
    groupids: List[str]
    hostids: List[str]
    output: OutputFormat


class MaintenanceCreateParams(TypedDict, total=False):
    """
    Parameters for maintenance.create API method

    active_since: Start time (Unix timestamp)
    active_till: End time (Unix timestamp)
    """
    name: str
    active_since: int
    active_till: int
    groupids: List[str]
    hostids: List[str]
    timeperiods: List[TimePeriod]
    description: str


class MaintenanceUpdateParams(TypedDict, total=False):
    """Parameters for maintenance.update API method"""
    maintenanceid: str
    name: str
    active_since: int
    active_till: int
    description: str


def maintenance_get(params: Optional[MaintenanceGetParams] = None) -> str:
    """
    Get maintenance periods from Zabbix

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of maintenance periods
    """
    if params is None:
        params = {}
    return zabbix_request('maintenance.get', params)


def maintenance_create(params: MaintenanceCreateParams) -> str:
    """
    Create a new maintenance period in Zabbix

    Args:
        params: Maintenance creation parameters

    Returns:
        JSON string containing creation result
    """
    return zabbix_request('maintenance.create', params)


def maintenance_update(params: MaintenanceUpdateParams) -> str:
    """
    Update an existing maintenance period in Zabbix

    Args:
        params: Maintenance update parameters

    Returns:
        JSON string containing update result
    """
    return zabbix_request('maintenance.update', params)


def maintenance_delete(maintenanceids: List[str]) -> str:
    """
    Delete maintenance periods from Zabbix

    Args:
        maintenanceids: List of maintenance IDs to delete

    Returns:
        JSON string containing deletion result
    """
    return zabbix_request('maintenance.delete', {'maintenanceids': maintenanceids})
