"""
Trend Data Retrieval Tools for Zabbix

Example:
    from lib.zabbix_py.trend import trend_get

    # Get trend data for specific items
    trends = trend_get({
        'itemids': ['12345'],
        'time_from': 1640995200,
        'limit': 100
    })
"""

from typing import TypedDict, List
from .utils import zabbix_request


class TrendGetParams(TypedDict, total=False):
    """Parameters for trend.get API method"""
    itemids: List[str]
    time_from: int
    time_till: int
    limit: int


def trend_get(params: TrendGetParams) -> str:
    """
    Get trend data from Zabbix

    Args:
        params: Trend retrieval parameters

    Returns:
        JSON string containing trend data
    """
    return zabbix_request('trend.get', params)
