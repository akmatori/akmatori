"""
Trigger Management Tools for Zabbix

Example:
    from lib.zabbix_py.trigger import trigger_get, trigger_create

    # Get all triggers for a host
    triggers = trigger_get({'hostids': ['10001']})

    # Create a new trigger
    result = trigger_create({
        'description': 'High CPU Load',
        'expression': '{host:system.cpu.load.avg(5m)}>5',
        'priority': 3
    })
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria
from .utils import zabbix_request


class TriggerGetParams(TypedDict, total=False):
    """Parameters for trigger.get API method"""
    triggerids: List[str]
    hostids: List[str]
    groupids: List[str]
    templateids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria
    limit: int


class TriggerCreateParams(TypedDict, total=False):
    """Parameters for trigger.create API method"""
    description: str
    expression: str
    priority: int
    status: int
    comments: str


class TriggerUpdateParams(TypedDict, total=False):
    """Parameters for trigger.update API method"""
    triggerid: str
    description: str
    expression: str
    priority: int
    status: int


def trigger_get(params: Optional[TriggerGetParams] = None) -> str:
    """
    Get triggers from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of triggers
    """
    if params is None:
        params = {}
    return zabbix_request('trigger.get', params)


def trigger_create(params: TriggerCreateParams) -> str:
    """
    Create a new trigger in Zabbix

    Args:
        params: Trigger creation parameters

    Returns:
        JSON string containing creation result
    """
    return zabbix_request('trigger.create', params)


def trigger_update(params: TriggerUpdateParams) -> str:
    """
    Update an existing trigger in Zabbix

    Args:
        params: Trigger update parameters

    Returns:
        JSON string containing update result
    """
    return zabbix_request('trigger.update', params)


def trigger_delete(triggerids: List[str]) -> str:
    """
    Delete triggers from Zabbix

    Args:
        triggerids: List of trigger IDs to delete

    Returns:
        JSON string containing deletion result
    """
    return zabbix_request('trigger.delete', {'triggerids': triggerids})
