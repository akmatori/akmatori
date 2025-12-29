"""
History Data Retrieval Tools for Zabbix

Example:
    from lib.zabbix_py.history import history_get

    # Get history for specific items
    history = history_get({
        'itemids': ['12345'],
        'time_from': 1640995200,
        'limit': 100
    })
"""

from typing import TypedDict, List
from .utils import zabbix_request


class HistoryGetParams(TypedDict, total=False):
    """
    Parameters for history.get API method

    history: History type (0=float, 1=character, 2=log, 3=unsigned, 4=text)
    """
    itemids: List[str]
    history: int
    time_from: int
    time_till: int
    limit: int
    sortfield: str
    sortorder: str


def history_get(params: HistoryGetParams) -> str:
    """
    Get history data from Zabbix

    Args:
        params: History retrieval parameters

    Returns:
        JSON string containing history data
    """
    return zabbix_request('history.get', params)
