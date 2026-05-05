import { useStore } from '../store'
import SearchBar from './SearchBar'
import ThreadList from './ThreadList'
import SearchResults from '../pages/SearchResults'

export default function MailListPane() {
  const search = useStore(s => s.search)
  const trimmed = search.trim()
  return (
    <>
      <SearchBar />
      {trimmed ? <SearchResults query={trimmed} /> : <ThreadList />}
    </>
  )
}
