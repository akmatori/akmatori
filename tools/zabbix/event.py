"""
Event Management Tools for Zabbix

Example:
    from lib.zabbix_py.event import event_get, event_acknowledge

    # Get recent events
    events = event_get({'limit': 100})

    # Acknowledge an event
    result = event_acknowledge({
        'eventids': ['12345'],
        'message': 'Working on it'
    })
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat
from .utils import zabbix_request


class EventGetParams(TypedDict, total=False):
    """Parameters for event.get API method"""
    eventids: List[str]
    groupids: List[str]
    hostids: List[str]
    objectids: List[str]
    output: OutputFormat
    time_from: int
    time_till: int
    limit: int


class EventAcknowledgeParams(TypedDict, total=False):
    """Parameters for event.acknowledge API method"""
    eventids: List[str]
    action: int
    message: str


def event_get(params: Optional[EventGetParams] = None) -> str:
    """
    Get events from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of events
    """
    if params is None:
        params = {}
    return zabbix_request('event.get', params)


def event_acknowledge(params: EventAcknowledgeParams) -> str:
    """
    Acknowledge events in Zabbix

    Args:
        params: Event acknowledgement parameters

    Returns:
        JSON string containing acknowledgement result
    """
    return zabbix_request('event.acknowledge', params)
