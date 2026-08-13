import { Component } from 'react'

class ErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { error: null }
  }

  static getDerivedStateFromError(error) {
    return { error }
  }

  componentDidCatch(error, info) {
    console.error('Render error:', error, info)
  }

  render() {
    if (this.state.error) {
      return (
        <div style={{
          minHeight: '100vh',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: '12px',
          background: '#0d1117',
          color: '#e6edf3',
          fontFamily: 'system-ui, sans-serif',
          padding: '24px',
          textAlign: 'center',
        }}>
          <div style={{ fontSize: '15px', fontWeight: 600 }}>界面渲染出错</div>
          <div style={{
            fontSize: '12px',
            color: '#ff8a8a',
            background: 'rgba(255, 82, 82, 0.08)',
            border: '1px solid rgba(255, 82, 82, 0.25)',
            borderRadius: '8px',
            padding: '10px 14px',
            maxWidth: '480px',
            wordBreak: 'break-all',
            whiteSpace: 'pre-wrap',
          }}>
            {String(this.state.error && this.state.error.message || this.state.error)}
          </div>
          <button
            onClick={() => window.location.reload()}
            style={{
              background: 'rgba(61, 220, 132, 0.12)',
              border: '1px solid rgba(61, 220, 132, 0.3)',
              color: '#3ddc84',
              borderRadius: '8px',
              padding: '9px 24px',
              fontSize: '13px',
              cursor: 'pointer',
            }}
          >
            重新加载
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

export default ErrorBoundary
