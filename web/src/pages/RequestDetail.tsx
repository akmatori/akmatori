import { useParams } from 'react-router-dom';
import RequestPanel from '../components/requests/RequestPanel';

// RequestDetail is the page of one request (/requests/:uuid). Requests are
// listed on the Events page (Requests filter).
export default function RequestDetail() {
  const { uuid } = useParams<{ uuid: string }>();
  return (
    <div className="flex flex-col min-h-0 h-full rounded-xl border border-line bg-surface overflow-hidden">
      {uuid && <RequestPanel key={uuid} uuid={uuid} />}
    </div>
  );
}
