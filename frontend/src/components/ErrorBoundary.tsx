import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

// ErrorBoundary catches render-time exceptions in any descendant. Without it,
// any throw in ThreadList / MessageBody / SearchResults unmounts the whole
// tree and leaves the user staring at a blank wails:// page with no
// affordance to recover.
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('App error boundary caught:', error, info)
  }

  reset = () => this.setState({ error: null })

  render() {
    if (this.state.error) {
      return (
        <div className="min-h-screen bg-zinc-950 text-zinc-100 flex items-center justify-center p-6">
          <div className="max-w-md space-y-4 text-sm">
            <h1 className="text-lg font-semibold">spk-mail hit an error</h1>
            <p className="text-zinc-400">
              Something in the UI threw an exception and rendering stopped. Your messages on disk are safe.
            </p>
            <pre className="whitespace-pre-wrap rounded border border-zinc-800 bg-zinc-900 p-3 text-xs text-rose-300">
              {this.state.error.message}
            </pre>
            <div className="flex gap-2">
              <button
                onClick={this.reset}
                className="rounded bg-blue-600 hover:bg-blue-500 px-3 py-1.5">
                Try again
              </button>
              <button
                onClick={() => window.location.reload()}
                className="rounded border border-zinc-700 hover:bg-zinc-800 px-3 py-1.5">
                Reload
              </button>
            </div>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
