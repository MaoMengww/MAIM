import { Fragment } from 'react';

// Elasticsearch highlights are untrusted text; only its literal emphasis markers have meaning.
export function SearchHighlight({ text }: { text: string }) {
  return <>{text.split(/(<em(?: class="search-highlight")?>.*?<\/em>)/gs).map((part, index) => {
    const emphasis = /^<em(?: class="search-highlight")?>(.*?)<\/em>$/s.exec(part);
    return emphasis ? <mark key={index}>{emphasis[1]}</mark> : <Fragment key={index}>{part}</Fragment>;
  })}</>;
}
