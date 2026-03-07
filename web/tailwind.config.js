/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx}'],
  theme: {
    extend: {
      fontFamily: {
        mono: ['"JetBrains Mono"', '"Fira Code"', 'monospace'],
        sans: ['"DM Sans"', 'system-ui', 'sans-serif'],
        display: ['"Outfit"', 'system-ui', 'sans-serif'],
      },
      colors: {
        forge: {
          50: '#fef7ec',
          100: '#fcecc9',
          200: '#f9d68e',
          300: '#f5b94d',
          400: '#f2a024',
          500: '#e8820e',
          600: '#cd5f09',
          700: '#aa420c',
          800: '#8a3410',
          900: '#722c11',
        },
        steel: {
          50: '#f6f7f9',
          100: '#eceef2',
          200: '#d4d9e3',
          300: '#afb8ca',
          400: '#8492ac',
          500: '#657593',
          600: '#505e7a',
          700: '#424d63',
          800: '#394254',
          900: '#333a47',
          950: '#14161d',
        },
      },
      animation: {
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
    },
  },
  plugins: [],
}
