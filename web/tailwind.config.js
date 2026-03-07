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
        // Matrix green — replaces the former orange "forge" palette
        forge: {
          50:  '#f0fff4',
          100: '#dcfce7',
          200: '#bbf7d0',
          300: '#86efac',
          400: '#00ff41',  // classic Matrix green
          500: '#00cc33',
          600: '#009926',
          700: '#007a1f',
          800: '#005c16',
          900: '#003d0e',
        },
        // Green-tinted blacks
        steel: {
          50:  '#f0f7f0',
          100: '#dceadc',
          200: '#bbd8bb',
          300: '#96be96',
          400: '#6da06d',
          500: '#4d7a4d',
          600: '#2e4d2e',
          700: '#1e341e',
          800: '#172617',
          900: '#0d1a0d',
          950: '#070d07',
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
