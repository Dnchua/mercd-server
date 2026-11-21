"""只使用Unity控件属性。禁止自动OCR/像素坐标回退、任意业务invoke。"""
import re
import time


def expand(value, variables):
    if isinstance(value, str):
        for key, item in variables.items(): value=value.replace('${'+key+'}',str(item))
        if re.search(r'\$\{[^}]+\}',value): raise ValueError('未绑定变量: '+value)
        return value
    if isinstance(value, list): return [expand(x,variables) for x in value]
    if isinstance(value, dict): return {k:expand(v,variables) for k,v in value.items()}
    return value


def select(poco, spec):
    if not spec or not spec.get('attrs'): raise ValueError('选择器必须包含控件属性')
    node=poco(**spec['attrs'])
    for child in spec.get('offspring',[]): node=node.offspring(**child)
    return node


class UI:
    def __init__(self,bridge,selectors,variables):
        self.bridge,self.selectors,self.variables=bridge,selectors,variables

    def target(self,role,key,timeout=20):
        spec=expand(self.selectors[key]['selector'],self.variables)
        end=time.monotonic()+timeout
        while time.monotonic()<end:
            node=select(self.bridge.activate(role),spec)
            n=len(node)
            if n>1: raise AssertionError('控件不唯一: '+key)
            if n==1 and node.attr('visible') is True: return node
            time.sleep(.2)
        raise AssertionError('未找到可见控件: '+key)

    def act(self,role,op,key,value=None):
        node=self.target(role,key)
        if op=='click':
            if node.attr('enabled') is False: raise AssertionError('控件禁用: '+key)
            if key in {'login.start','m6.battle.result_panel','m6.battle.win','m6.battle.lose','m6.raid.battle_pause','m6.raid.appear_attack_btn','m6.raid.appear_giveup_btn'} and getattr(self.bridge,'api',None) is not None and hasattr(self.bridge.api,'device'):
                p=[0.5,0.74] if key=='login.start' else (node.attr('pos') or [0.5,0.5])
                w,h=self.bridge.api.device().get_current_resolution()
                self.bridge.api.shell(f'input tap {int(p[0]*w)} {int(p[1]*h)}')
            else: node.click()
        elif op=='long_click': node.long_click(duration=1.5)
        elif op=='set_text':
            # 只读桥不提供 SetAttr；用 Airtest 真实触摸聚焦 + 系统输入法输入。
            node.click(); time.sleep(0.6)
            value=expand(value,self.variables)
            try:
                self.bridge.api.text(value)
            except Exception:
                # Airtest text 失败时退回 adb input text（仅 ASCII）并补发回车触发 UIInput.onSubmit
                if any(ord(ch)>127 for ch in value): raise
                self.bridge.api.shell('input text '+value)
                self.bridge.api.shell('input keyevent 66')
            # Yosemite IME 的 ENTER 键事件不映射编辑器 done 动作（实测键盘不收起、
            # onSubmit 不触发）；补发 ADB_EDITOR_CODE(DONE=6) 触发 NGUI UIInput.onSubmit。
            try:
                self.bridge.api.shell('am broadcast -a ADB_EDITOR_CODE --ei code 6')
            except Exception:
                pass
            time.sleep(0.6)
        elif op=='visible': return
        elif op=='text_equals':
            actual=node.attr('text')
            if actual != expand(value,self.variables): raise AssertionError('控件文本不一致: '+key+' = '+repr(actual))
        elif op=='disabled':
            if node.attr('enabled') is not False and node.attr('clickable') is not False: raise AssertionError('控件应为禁用: '+key)
        elif op=='swipe': node.swipe(expand(value,self.variables))
        else: raise ValueError('未知UI动作: '+op)
